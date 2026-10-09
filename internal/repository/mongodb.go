package repository

import (
	"context"
	"fmt"
	"go-learn/internal/model"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoRepository 实现了 Repository 接口
type MongoRepository struct {
	client      *mongo.Client
	sessionsCol *mongo.Collection
	messagesCol *mongo.Collection
}

// NewMongoRepository 构造函数
func NewMongoRepository(client *mongo.Client, dbName string) *MongoRepository {
	db := client.Database(dbName)
	return &MongoRepository{
		client:      client,
		sessionsCol: db.Collection("sessions"),
		messagesCol: db.Collection("messages"),
	}
}

// CreateSession 插入新会话
func (m *MongoRepository) CreateSession(ctx context.Context, session *model.Session) error {
	_, err := m.sessionsCol.InsertOne(ctx, session)
	if err != nil {
		return fmt.Errorf("failed to insert session: %w", err)
	}
	return nil
}

// GetSession 按 ID 获取会话
func (m *MongoRepository) GetSession(ctx context.Context, id string) (*model.Session, error) {
	var session model.Session
	filter := bson.M{"_id": id}

	err := m.sessionsCol.FindOne(ctx, filter).Decode(&session)
	if err != nil {
		return nil, fmt.Errorf("failed to find session: %w", err)
	}
	return &session, nil
}

// SaveMessage 保存对话消息
func (m *MongoRepository) SaveMessage(ctx context.Context, msg *model.Message) error {
	_, err := m.messagesCol.InsertOne(ctx, msg)
	if err != nil {
		return fmt.Errorf("failed to insert message: %w", err)
	}
	return nil
}

// GetSessionMessages 查询某会话的历史消息，按创建时间正序排列
func (m *MongoRepository) GetSessionMessages(ctx context.Context, sessionID string) ([]model.Message, error) {
	filter := bson.M{"session_id": sessionID}
	// 按照时间正序排
	findOpts := options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}})

	cursor, err := m.messagesCol.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer cursor.Close(ctx) // 确保游标在退出前释放

	var messages []model.Message
	if err := cursor.All(ctx, &messages); err != nil {
		return nil, fmt.Errorf("failed to decode messages: %w", err)
	}

	return messages, nil
}
