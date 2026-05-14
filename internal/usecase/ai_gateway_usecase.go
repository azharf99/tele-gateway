// internal/usecase/ai_gateway_usecase.go
package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"google.golang.org/genai"
	"go.uber.org/zap"
)

type userQueue struct {
	messages []string
	timer    *time.Timer
	mu       sync.Mutex
}

type aiGatewayUseCase struct {
	repo        domain.AIContextRepository
	genaiClient *genai.Client
	apiKey      string
	logger      *zap.Logger
	queues      sync.Map // map[int64]*userQueue
}

func NewAIGatewayUseCase(repo domain.AIContextRepository, apiKey string, logger *zap.Logger) (domain.AIGatewayUseCase, error) {
	ctx := context.Background()
	// Using the newest Google Gen AI SDK (google.golang.org/genai)
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &aiGatewayUseCase{
		repo:        repo,
		genaiClient: client,
		apiKey:      apiKey,
		logger:      logger,
	}, nil
}

func (u *aiGatewayUseCase) HandlePrivateMessage(ctx context.Context, senderID int64, senderName string, text string, replyFunc func(string) error) error {
	qRaw, _ := u.queues.LoadOrStore(senderID, &userQueue{})
	q := qRaw.(*userQueue)

	q.mu.Lock()
	defer q.mu.Unlock()

	q.messages = append(q.messages, text)

	// Reset timer (Debounce logic)
	if q.timer != nil {
		q.timer.Stop()
	}

	q.timer = time.AfterFunc(1*time.Minute, func() {
		u.processQueue(senderID, replyFunc)
	})

	u.logger.Info("Message queued for AI", zap.Int64("sender_id", senderID), zap.String("sender_name", senderName))
	return nil
}

func (u *aiGatewayUseCase) processQueue(senderID int64, replyFunc func(string) error) {
	qRaw, ok := u.queues.Load(senderID)
	if !ok {
		return
	}
	q := qRaw.(*userQueue)

	q.mu.Lock()
	if len(q.messages) == 0 {
		q.mu.Unlock()
		return
	}

	// Batch limit: 10 messages
	limit := 10
	if len(q.messages) < limit {
		limit = len(q.messages)
	}

	batch := q.messages[:limit]
	q.messages = q.messages[limit:]

	// If more than 10 messages remain, schedule another 1-minute timer (as per user request)
	if len(q.messages) > 0 {
		q.timer = time.AfterFunc(1*time.Minute, func() {
			u.processQueue(senderID, replyFunc)
		})
	} else {
		q.timer = nil
	}
	q.mu.Unlock()

	// Process the batch with AI
	u.logger.Info("Processing AI batch", zap.Int64("sender_id", senderID), zap.Int("batch_size", len(batch)))
	
	// Get System Prompt from DB
	systemPrompt := "You are a helpful assistant."
	aiCtx, err := u.repo.Get("system_prompt")
	if err == nil && aiCtx != nil {
		systemPrompt = aiCtx.Value
	}

	combinedMessages := strings.Join(batch, "\n")
	
	// Prepare content for Gemini
	ctx := context.Background()
	// Using model gemini-2.5-flash
	resp, err := u.genaiClient.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(fmt.Sprintf("%s\n\nUser messages:\n%s", systemPrompt, combinedMessages)), nil)
	if err != nil {
		u.logger.Error("Gemini API error", zap.Error(err))
		return // Do not send error message to avoid leaking sensitive info
	}

	var aiResponse string
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Text != "" {
				aiResponse += part.Text
			}
		}
	}

	if aiResponse == "" {
		u.logger.Warn("Gemini returned empty response")
		return // Do not send empty response fallback
	}

	aiResponse = fmt.Sprintf("%s\n\n[Dibalas oleh Azhar's AI Assistant]", strings.TrimSpace(aiResponse))

	err = replyFunc(aiResponse)
	if err != nil {
		u.logger.Error("Failed to send AI response", zap.Error(err))
	}
}

func (u *aiGatewayUseCase) SetAIContext(key, value string) error {
	return u.repo.Set(&domain.AIContext{
		Key:      key,
		Value:    value,
		IsActive: true,
	})
}

func (u *aiGatewayUseCase) GetAIContext(key string) (string, error) {
	aiCtx, err := u.repo.Get(key)
	if err != nil {
		return "", err
	}
	return aiCtx.Value, nil
}
