package queue

import (
	"context"
	"go-learn/internal/model"
)

// Producer 消息生产者接口
type Producer interface {
	PublishMemoryTask(ctx context.Context, task *model.MemoryTask) error
	Close() error
}

// Consumer 消息消费者接口
type Consumer interface {
	Start(ctx context.Context) error
	Close() error
}
