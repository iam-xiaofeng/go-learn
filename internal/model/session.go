package model

import "time"

// 角色枚举：Go 没有 enum 关键字，用 typed string + const 模拟
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message 代表一条对话消息
// 注意后面的反引号标签：
// json:"role"   -> 传给前端/API 时转换为小写下划线
// bson:"role"   -> 存入 MongoDB 时的文档字段名
type Message struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	SessionID string    `json:"session_id" bson:"session_id"`
	Role      Role      `json:"role" bson:"role"`
	Content   string    `json:"content" bson:"content"`
	Tokens    int       `json:"tokens" bson:"tokens"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
}

// Session 代表一个 Agent 会话
type Session struct {
	ID        string    `json:"id" bson:"_id,omitempty"`
	UserID    string    `json:"user_id" bson:"user_id"`
	Title     string    `json:"title" bson:"title"`
	CreatedAt time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bson:"updated_at"`
}

// MemoryTask 发送到 RabbitMQ 的异步任务载荷
type MemoryTask struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Role      Role   `json:"role"`
}
