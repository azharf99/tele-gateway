// internal/usecase/message_usecase.go
package usecase

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
)

const maxTelegramMessageRunes = 4096

type MessageSender interface {
	SendMessage(ctx context.Context, chatID int64, topicID int, username string, text string) error
}

type BotStatusProvider interface {
	GetStatus() string
}

type messageUseCase struct {
	sender         MessageSender
	statusProvider BotStatusProvider
	logger         *zap.Logger
}

func NewMessageUseCase(sender MessageSender, statusProvider BotStatusProvider, logger *zap.Logger) domain.MessageUseCase {
	return &messageUseCase{
		sender:         sender,
		statusProvider: statusProvider,
		logger:         logger,
	}
}

func (u *messageUseCase) SendMessage(ctx context.Context, req domain.SendMessageRequest) error {
	msg := strings.TrimSpace(req.Message)
	if msg == "" || utf8.RuneCountInString(msg) > maxTelegramMessageRunes {
		return fmt.Errorf("%w: message must be 1-%d characters", domain.ErrInvalidTarget, maxTelegramMessageRunes)
	}

	username := strings.TrimSpace(req.Username)
	hasChat := req.ChatID != 0
	hasUser := username != ""
	if hasChat == hasUser {
		return fmt.Errorf("%w: provide exactly one of chat_id or username", domain.ErrInvalidTarget)
	}

	if req.TopicID < 0 {
		return fmt.Errorf("%w: topic_id must be >= 0", domain.ErrInvalidTarget)
	}

	if u.statusProvider != nil && u.statusProvider.GetStatus() != "RUNNING" {
		return domain.ErrBotNotRunning
	}

	if err := u.sender.SendMessage(ctx, req.ChatID, req.TopicID, username, msg); err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}

	if u.logger != nil {
		u.logger.Info("B2B Telegram message sent",
			zap.Int64("chat_id", req.ChatID),
			zap.Int("topic_id", req.TopicID),
			zap.Bool("via_username", hasUser),
		)
	}
	return nil
}
