package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof" // 注册 pprof 性能分析路由

	"go-learn/internal/handler"
	"go-learn/internal/queue"
	"go-learn/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	log.Println("🚀 Starting Agent Session & Memory Backend Service...")

	// 0. 启动 pprof 性能分析服务器 (独立端口 6060，便于火焰图抓取)
	go func() {
		log.Println("🔍 [pprof] Profiler listening on http://localhost:6060/debug/pprof")
		if err := http.ListenAndServe("localhost:6060", nil); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[pprof] Server failed: %v\n", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. 初始化持久化层 (MongoDB 或内存回退)
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	var repo repository.Repository
	mongoCtx, cancelMongo := context.WithTimeout(ctx, 1*time.Second)
	defer cancelMongo()

	// 调优 MongoDB 连接池：默认 100 -> 调大至 300，避免 -c 100 时排队争抢
	mongoOpts := options.Client().
		ApplyURI(mongoURI).
		SetMaxPoolSize(300).
		SetMinPoolSize(50)

	client, err := mongo.Connect(mongoOpts)
	if err == nil && client.Ping(mongoCtx, nil) == nil {
		log.Println("✅ [Database] Successfully connected to MongoDB (PoolSize: 300) at:", mongoURI)
		repo = repository.NewMongoRepository(client, "agent_memory_db")
	} else {
		log.Println("⚠️  [Database] MongoDB not reachable, using Thread-Safe In-Memory Repository.")
		repo = repository.NewMemoryRepository()
	}

	// 2. 初始化 Redis 缓存层（如果可用，包装成缓存装饰器）
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "localhost:6379"
	}
	// 调优 Redis 连接池：调大至 300，设置最小空闲连接 50
	rdb := redis.NewClient(&redis.Options{
		Addr:         redisURL,
		PoolSize:     300,
		MinIdleConns: 50,
	})
	redisCtx, cancelRedis := context.WithTimeout(ctx, 1*time.Second)
	defer cancelRedis()
	if rdb.Ping(redisCtx).Err() == nil {
		log.Println("✅ [Cache] Successfully connected to Redis (PoolSize: 300) at:", redisURL)
		repo = repository.NewCachedRepository(repo, rdb, 30*time.Minute)
	} else {
		log.Println("⚠️  [Cache] Redis not reachable, skipping cache layer.")
	}

	// 3. 初始化消息队列 (两级高并发架构：进程内 50000 缓冲 + 10 独立 Publisher + 10 独立 Consumer)
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}

	var qProducer queue.Producer
	rmqService, err := queue.NewTwoStageRabbitMQService(rabbitURL, "agent.memory.tasks", 50000, 10, 10)
	if err == nil {
		log.Println("✅ [Queue] Successfully connected to Two-Stage RabbitMQ Engine at:", rabbitURL)
		qProducer = rmqService
		_ = rmqService.StartConsumers(ctx)
	} else {
		log.Println("⚠️  [Queue] RabbitMQ not reachable, using in-process Goroutine Channel Queue.")
		chanQueue := queue.NewChannelQueue(50000)
		chanQueue.StartConsumer(ctx)
		qProducer = chanQueue
	}
	defer qProducer.Close()

	// 4. 初始化 Web 路由与 HTTP Server (使用 gin.New() 避免同步 stdout 锁竞争)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	h := handler.NewHandler(repo, qProducer)
	h.RegisterRoutes(r)

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	// 4. 在独立 Goroutine 启动 HTTP 服务，防止阻塞主流程
	go func() {
		log.Println("🌐 [HTTP] Listening and serving on http://localhost:8080")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v\n", err)
		}
	}()

	// 5. 优雅停机等待：阻塞直到收到 Ctrl+C 或 SIGTERM
	<-ctx.Done()
	log.Println("\n🛑 Shutting down gracefully, waiting for requests to finish...")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server forced shutdown: %v\n", err)
	}

	log.Println("👋 Server exited cleanly.")
}
