// internal/usecase/ai_gateway_usecase.go
package usecase

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/parser"
	"go.uber.org/zap"
	"google.golang.org/genai"
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

func (u *aiGatewayUseCase) HandlePrivateMessage(ctx context.Context, accountID uint, senderID int64, senderName string, promptOverride string, text string, replyFunc func(string) error) error {
	queueKey := fmt.Sprintf("%d:%d", accountID, senderID)
	qRaw, ok := u.queues.Load(queueKey)
	var q *userQueue
	if !ok {
		q = &userQueue{}
		qRaw, _ = u.queues.LoadOrStore(queueKey, q)
		q = qRaw.(*userQueue)
	} else {
		q = qRaw.(*userQueue)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	q.messages = append(q.messages, text)

	// Reset timer (Debounce logic)
	if q.timer != nil {
		q.timer.Stop()
	}

	q.timer = time.AfterFunc(60*time.Second, func() {
		u.processQueue(queueKey, senderID, promptOverride, q, replyFunc)
	})

	u.logger.Info("Message queued for AI (60s debounce)",
		zap.Uint("account_id", accountID),
		zap.Int64("sender_id", senderID),
		zap.String("sender_name", senderName),
	)
	return nil
}

func (u *aiGatewayUseCase) processQueue(queueKey string, senderID int64, promptOverride string, q *userQueue, replyFunc func(string) error) {
	q.mu.Lock()
	if len(q.messages) == 0 {
		q.timer = nil
		u.queues.CompareAndDelete(queueKey, q)
		q.mu.Unlock()
		return
	}

	// Batch limit: 10 messages
	limit := 10
	if len(q.messages) < limit {
		limit = len(q.messages)
	}

	batch := make([]string, limit)
	copy(batch, q.messages[:limit])

	// Fix slice memory leak by allocating a new slice
	q.messages = append([]string(nil), q.messages[limit:]...)

	// If more than 10 messages remain, schedule another 10-second timer
	if len(q.messages) > 0 {
		q.timer = time.AfterFunc(10*time.Second, func() {
			u.processQueue(queueKey, senderID, promptOverride, q, replyFunc)
		})
	} else {
		q.timer = nil
		u.queues.CompareAndDelete(queueKey, q)
	}
	q.mu.Unlock()

	// Process the batch with AI
	u.logger.Info("Processing AI batch", zap.String("queue_key", queueKey), zap.Int64("sender_id", senderID), zap.Int("batch_size", len(batch)))

	// Get System Prompt (account override first, then global DB fallback)
	systemPrompt := strings.TrimSpace(promptOverride)
	if systemPrompt == "" {
		systemPrompt = "You are a helpful assistant."
		aiCtx, err := u.repo.Get("system_prompt")
		nowWIB := time.Now().In(parser.WIBLocation()).Format("Monday, 02 Jan 2006 15:04 WIB")

		if err == nil && aiCtx != nil && strings.TrimSpace(aiCtx.Value) != "" {
			systemPrompt = fmt.Sprintf(`%s\n\nWaktu Saat ini (WIB): %s`, aiCtx.Value, nowWIB)
		}
	}

	combinedMessages := strings.Join(batch, "\n")

	// Prepare content for Gemini with proper SystemInstruction separation
	// SECURITY: System prompt is passed via SystemInstruction (not as user content)
	// to prevent the model from leaking it in the response.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	temperature := float32(0.4)
	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, ""),
		Temperature:       &temperature,
	}

	resp, err := u.genaiClient.Models.GenerateContent(ctx, "gemini-3.1-flash-lite", genai.Text(combinedMessages), config)
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

	aiResponse = fmt.Sprintf("%s\n\n[Azhar's AI Assistant]", strings.TrimSpace(aiResponse))

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
