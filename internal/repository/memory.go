package repository

import (
	"context"
	"fmt"
	"sync"

	"go-learn/internal/model"
)

// MemoryRepository 纯内存实现的 Repository，方便快速测试与单元测试
type MemoryRepository struct {
	mu       sync.RWMutex
	sessions map[string]*model.Session
	messages map[string][]model.Message // sessionID -> 消息列表
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		sessions: make(map[string]*model.Session),
		messages: make(map[string][]model.Message),
	}
}

func (m *MemoryRepository) CreateSession(ctx context.Context, session *model.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock() // 退出时自动解锁

	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryRepository) GetSession(ctx context.Context, id string) (*model.Session, error) {
	m.mu.RLock() // 共享读锁
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return s, nil
}

func (m *MemoryRepository) SaveMessage(ctx context.Context, msg *model.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.messages[msg.SessionID] = append(m.messages[msg.SessionID], *msg)
	return nil
}

func (m *MemoryRepository) GetSessionMessages(ctx context.Context, sessionID string) ([]model.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	msgs := m.messages[sessionID]
	// 返回切片副本
	copied := make([]model.Message, len(msgs))
	copy(copied, msgs)
	return copied, nil
}
