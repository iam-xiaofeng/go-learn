package repository

import (
	"context"
	"encoding/json"
	"time"

	"go-learn/internal/model"

	"github.com/redis/go-redis/v9"
)

// CachedRepository 采用装饰器模式：包装任意底层 Repository，增加 Redis 极速缓存
type CachedRepository struct {
	underlying Repository
	rdb        *redis.Client
	ttl        time.Duration
}

func NewCachedRepository(underlying Repository, rdb *redis.Client, ttl time.Duration) *CachedRepository {
	if ttl == 0 {
		ttl = 30 * time.Minute
	}
	return &CachedRepository{
		underlying: underlying,
		rdb:        rdb,
		ttl:        ttl,
	}
}

// CreateSession 先写 Mongo，再顺便预热写 Redis
func (c *CachedRepository) CreateSession(ctx context.Context, session *model.Session) error {
	if err := c.underlying.CreateSession(ctx, session); err != nil {
		return err
	}

	data, err := json.Marshal(session)
	if err == nil {
		_ = c.rdb.Set(ctx, "session:"+session.ID, data, c.ttl).Err()
	}
	return nil
}

// GetSession 核心优化点：优先读 Redis 内存！
func (c *CachedRepository) GetSession(ctx context.Context, id string) (*model.Session, error) {
	// 1. 尝试从 Redis 内存拿 (0.05 毫秒级)
	val, err := c.rdb.Get(ctx, "session:"+id).Bytes()
	if err == nil {
		var session model.Session
		if err := json.Unmarshal(val, &session); err == nil {
			return &session, nil // 🔥 命中缓存！瞬间返回，彻底砍掉那 25% 的 Mongo 磁盘开销！
		}
	}

	// 2. 缓存未命中：穿透查 MongoDB
	session, err := c.underlying.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}

	// 3. 回写 Redis 缓存，供后续请求秒级复用
	if data, err := json.Marshal(session); err == nil {
		_ = c.rdb.Set(ctx, "session:"+id, data, c.ttl).Err()
	}

	return session, nil
}

func (c *CachedRepository) SaveMessage(ctx context.Context, msg *model.Message) error {
	return c.underlying.SaveMessage(ctx, msg)
}

func (c *CachedRepository) SaveMessagesBatch(ctx context.Context, msgs []*model.Message) error {
	return c.underlying.SaveMessagesBatch(ctx, msgs)
}

func (c *CachedRepository) GetSessionMessages(ctx context.Context, sessionID string) ([]model.Message, error) {
	return c.underlying.GetSessionMessages(ctx, sessionID)
}
