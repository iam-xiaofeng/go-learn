package repository

import (
	"context"
	"go-learn/internal/model"
)

// Repository 规定了持久化层必须实现的契约
// 注意：所有方法第一个参数都是 ctx context.Context，用于传递请求超时与链路追踪
type Repository interface {
	CreateSession(ctx context.Context, session *model.Session) error
	GetSession(ctx context.Context, id string) (*model.Session, error)
	SaveMessage(ctx context.Context, msg *model.Message) error
	GetSessionMessages(ctx context.Context, sessionID string) ([]model.Message, error)
}
