package queue

import (
	"context"
	"log"

	"go-learn/internal/model"
)

// ChannelQueue 使用 Go 原生 Channel 模拟消息队列
type ChannelQueue struct {
	taskChan chan model.MemoryTask
}

func NewChannelQueue(bufferSize int) *ChannelQueue {
	return &ChannelQueue{
		taskChan: make(chan model.MemoryTask, bufferSize),
	}
}

func (c *ChannelQueue) PublishMemoryTask(ctx context.Context, task *model.MemoryTask) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.taskChan <- *task:
		log.Printf("[Channel Queue] Enqueued memory task for session: %s\n", task.SessionID)
		return nil
	}
}

// StartConsumer 启动后台消费 Goroutine
func (c *ChannelQueue) StartConsumer(ctx context.Context) {
	go func() {
		log.Println("[Channel Worker] Background memory worker started...")
		for {
			select {
			case <-ctx.Done():
				log.Println("[Channel Worker] Stopped by context.")
				return
			case task := <-c.taskChan:
				log.Printf("🔥 [Channel Worker] Async Extracted Memory: SessionID=%s, Role=%s, Content=%q\n",
					task.SessionID, task.Role, task.Content)
			}
		}
	}()
}

func (c *ChannelQueue) Close() error {
	close(c.taskChan)
	return nil
}
