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

// MultiAccountMessageSender routes proactive B2B/Watchdog messages to the
// designated B2B Telegram account managed by AccountManager.
type MultiAccountMessageSender interface {
	ResolveB2BAccount(requestedAccountID uint) (accountID uint, status domain.AccountStatus, err error)
	SendMessage(ctx context.Context, accountID uint, chatID int64, topicID int, username string, text string) error
}

type messageUseCase struct {
	sender MultiAccountMessageSender
	logger *zap.Logger
}

func NewMessageUseCase(sender MultiAccountMessageSender, logger *zap.Logger) domain.MessageUseCase {
	return &messageUseCase{
		sender: sender,
		logger: logger,
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

	accountID, status, err := u.sender.ResolveB2BAccount(req.AccountID)
	if err != nil {
		return err
	}
	if status != domain.AccountStatusRunning {
		return domain.ErrBotNotRunning
	}

	if err := u.sender.SendMessage(ctx, accountID, req.ChatID, req.TopicID, username, msg); err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}

	if u.logger != nil {
		u.logger.Info("B2B Telegram message sent",
			zap.Uint("account_id", accountID),
			zap.Int64("chat_id", req.ChatID),
			zap.Int("topic_id", req.TopicID),
			zap.Bool("via_username", hasUser),
		)
	}
	return nil
}
