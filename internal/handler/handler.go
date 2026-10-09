package handler

import (
	"net/http"
	"time"

	"go-learn/internal/model"
	"go-learn/internal/queue"
	"go-learn/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler 聚合了 Repository（数据库）和 Producer（消息队列）
// 这就是依赖注入（Dependency Injection），方便测试与替换实现
type Handler struct {
	repo  repository.Repository
	queue queue.Producer
}

func NewHandler(repo repository.Repository, queue queue.Producer) *Handler {
	return &Handler{
		repo:  repo,
		queue: queue,
	}
}

// RegisterRoutes 注册 HTTP 路由
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.POST("/sessions", h.CreateSession)
		api.GET("/sessions/:id", h.GetSession)
		api.POST("/sessions/:id/messages", h.SendMessage)
		api.GET("/sessions/:id/messages", h.GetMessages)
	}
}

type CreateSessionRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Title  string `json:"title" binding:"required"`
}

// CreateSession POST /api/sessions
func (h *Handler) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	// ShouldBindJSON 会自动反序列化并做基础校验 (binding:"required")
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	session := &model.Session{
		ID:        uuid.New().String(),
		UserID:    req.UserID,
		Title:     req.Title,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// 写入数据库
	if err := h.repo.CreateSession(c.Request.Context(), session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	c.JSON(http.StatusCreated, session)
}

// GetSession GET /api/sessions/:id
func (h *Handler) GetSession(c *gin.Context) {
	sessionID := c.Param("id")

	session, err := h.repo.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}

	c.JSON(http.StatusOK, session)
}

type SendMessageRequest struct {
	Role    model.Role `json:"role" binding:"required"`
	Content string     `json:"content" binding:"required"`
}

// SendMessage POST /api/sessions/:id/messages
func (h *Handler) SendMessage(c *gin.Context) {
	sessionID := c.Param("id")

	// 1. 验证 Session 是否存在
	_, err := h.repo.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	msg := &model.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      req.Role,
		Content:   req.Content,
		Tokens:    len(req.Content) / 2, // 模拟计算 token 数量
		CreatedAt: time.Now(),
	}

	// 2. 同步保存到数据库 (MongoDB)
	if err := h.repo.SaveMessage(c.Request.Context(), msg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save message"})
		return
	}

	// 3. 异步发送任务到消息队列 (RabbitMQ)
	// 注意：这里不需要等待任务处理完，发完即走！
	task := &model.MemoryTask{
		SessionID: sessionID,
		MessageID: msg.ID,
		Content:   msg.Content,
		Role:      msg.Role,
	}
	_ = h.queue.PublishMemoryTask(c.Request.Context(), task)

	c.JSON(http.StatusCreated, msg)
}

// GetMessages GET /api/sessions/:id/messages
func (h *Handler) GetMessages(c *gin.Context) {
	sessionID := c.Param("id")

	msgs, err := h.repo.GetSessionMessages(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get messages"})
		return
	}

	c.JSON(http.StatusOK, msgs)
}
