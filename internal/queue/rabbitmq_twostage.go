package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"go-learn/internal/model"

	amqp "github.com/rabbitmq/amqp091-go"
)

var ErrBufferFull = errors.New("memory task buffer queue is full")

// TwoStageRabbitMQService 工业级两级生产者-消费者服务
type TwoStageRabbitMQService struct {
	conn           *amqp.Connection
	queueName      string
	bufferChan     chan *model.MemoryTask // 第一级：进程内极速内存缓冲池 (L1)
	numPublishers  int                    // 专属 Publisher 协程数
	numConsumers   int                    // 专属 Consumer 协程数
	publisherWg    sync.WaitGroup
	consumerWg     sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc

	// 监控原子计数器
	PublishedCount atomic.Int64
	ConsumedCount  atomic.Int64
	DroppedCount   atomic.Int64
}

// NewTwoStageRabbitMQService 构造函数
func NewTwoStageRabbitMQService(amqpURL string, queueName string, bufferSize int, numPublishers int, numConsumers int) (*TwoStageRabbitMQService, error) {
	if bufferSize <= 0 {
		bufferSize = 50000
	}
	if numPublishers <= 0 {
		numPublishers = 10
	}
	if numConsumers <= 0 {
		numConsumers = 10
	}

	// 1. 建立 TCP 物理长连接 (全局仅此一根物理光缆)
	conn, err := amqp.Dial(amqpURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rabbitmq: %w", err)
	}

	// 2. 使用临时信道预先声明队列，保证持久化队列已就绪
	initCh, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to open init channel: %w", err)
	}
	_, err = initCh.QueueDeclare(
		queueName,
		true,  // durable 持久化
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,
	)
	_ = initCh.Close()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to declare durable queue: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	svc := &TwoStageRabbitMQService{
		conn:          conn,
		queueName:     queueName,
		bufferChan:    make(chan *model.MemoryTask, bufferSize),
		numPublishers: numPublishers,
		numConsumers:  numConsumers,
		ctx:           ctx,
		cancel:        cancel,
	}

	// 3. 启动后台 Publisher 协程池 (多 Channel 并发向 RabbitMQ 推流)
	svc.startPublishers()

	return svc, nil
}

// PublishMemoryTask 第一级生产：HTTP 接待员直接往内存 channel 丢指针，0.0001ms 极速返回！
func (s *TwoStageRabbitMQService) PublishMemoryTask(ctx context.Context, task *model.MemoryTask) error {
	select {
	case s.bufferChan <- task:
		// 极速入队成功，主线程瞬间返回
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		// 极端洪峰熔断降级：防止内存无限制撑爆，绝不阻塞用户 HTTP 请求
		s.DroppedCount.Add(1)
		return ErrBufferFull
	}
}

// startPublishers 启动多信道 Publisher 协程池
func (s *TwoStageRabbitMQService) startPublishers() {
	for i := 0; i < s.numPublishers; i++ {
		workerID := i
		// 🔥 核心重构：每个 Publisher 独享自己专属的 *amqp.Channel！
		ch, err := s.conn.Channel()
		if err != nil {
			log.Printf("[Error] Failed to open publisher channel %d: %v\n", workerID, err)
			continue
		}

		s.publisherWg.Add(1)
		go func(workerID int, workerCh *amqp.Channel) {
			defer s.publisherWg.Done()
			defer workerCh.Close()

			for {
				select {
				case <-s.ctx.Done():
					return
				case task, ok := <-s.bufferChan:
					if !ok {
						return // 缓冲区关闭，正常退出
					}

					body, err := json.Marshal(task)
					if err != nil {
						continue
					}

					// 独立信道发布：完全零锁争抢！
					err = workerCh.PublishWithContext(
						s.ctx,
						"",
						s.queueName,
						false,
						false,
						amqp.Publishing{
							DeliveryMode: amqp.Persistent, // 持久化落盘
							ContentType:  "application/json",
							Body:         body,
						},
					)
					if err == nil {
						s.PublishedCount.Add(1)
					}
				}
			}
		}(workerID, ch)
	}
}

// StartConsumers 启动多信道 Consumer 消费者池 (多柜台并行开工)
func (s *TwoStageRabbitMQService) StartConsumers(ctx context.Context) error {
	for i := 0; i < s.numConsumers; i++ {
		workerID := i
		// 🔥 核心重构：每个 Consumer 独享自己专属的 *amqp.Channel！
		ch, err := s.conn.Channel()
		if err != nil {
			return fmt.Errorf("failed to open consumer channel %d: %w", workerID, err)
		}

		// 调优 QoS：预取 50 条消息，开启吞吐量流水线 (Pipelining)
		if err := ch.Qos(50, 0, false); err != nil {
			_ = ch.Close()
			return err
		}

		msgs, err := ch.Consume(
			s.queueName,
			fmt.Sprintf("worker-%d", workerID),
			false, // 手动 ACK，确保断电不丢数据
			false,
			false,
			false,
			nil,
		)
		if err != nil {
			_ = ch.Close()
			return err
		}

		s.consumerWg.Add(1)
		go func(workerID int, workerCh *amqp.Channel, deliveryChan <-chan amqp.Delivery) {
			defer s.consumerWg.Done()
			defer workerCh.Close()

			for {
				select {
				case <-ctx.Done():
					return
				case d, ok := <-deliveryChan:
					if !ok {
						return
					}

					// 模拟提取记忆业务
					s.ConsumedCount.Add(1)

					// 确认消费完毕，从 MQ 安全删除
					_ = d.Ack(false)
				}
			}
		}(workerID, ch, msgs)
	}

	log.Printf("🚀 [Two-Stage MQ] Started %d Dedicated Publishers & %d Dedicated Consumers with Multi-Channels.\n",
		s.numPublishers, s.numConsumers)
	return nil
}

// Close 优雅安全停机
func (s *TwoStageRabbitMQService) Close() error {
	s.cancel()

	// 等待所有后台协程安全落盘退出
	done := make(chan struct{})
	go func() {
		s.publisherWg.Wait()
		s.consumerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Println("⚠️ [Two-Stage MQ] Timeout waiting for workers to exit, forcing close.")
	}

	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}
