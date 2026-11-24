// internal/usecase/message_usecase_test.go
package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
)

type stubMultiAccountSender struct {
	resolvedAccountID uint
	resolvedStatus    domain.AccountStatus
	resolveErr        error

	called    bool
	accountID uint
	chatID    int64
	topicID   int
	username  string
	text      string
	sendErr   error
}

func (s *stubMultiAccountSender) ResolveB2BAccount(requestedAccountID uint) (uint, domain.AccountStatus, error) {
	if s.resolveErr != nil {
		return 0, domain.AccountStatusStopped, s.resolveErr
	}
	if requestedAccountID > 0 {
		return requestedAccountID, s.resolvedStatus, nil
	}
	return s.resolvedAccountID, s.resolvedStatus, nil
}

func (s *stubMultiAccountSender) SendMessage(_ context.Context, accountID uint, chatID int64, topicID int, username string, text string) error {
	s.called = true
	s.accountID = accountID
	s.chatID = chatID
	s.topicID = topicID
	s.username = username
	s.text = text
	return s.sendErr
}

func TestMessageUseCase_Validation(t *testing.T) {
	tooLong := strings.Repeat("a", maxTelegramMessageRunes+1)
	tests := []struct {
		name       string
		status     domain.AccountStatus
		resolveErr error
		req        domain.SendMessageRequest
		want       error
	}{
		{
			name:   "empty message",
			status: domain.AccountStatusRunning,
			req:    domain.SendMessageRequest{ChatID: 123, Message: "   "},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "message exceeds max runes",
			status: domain.AccountStatusRunning,
			req:    domain.SendMessageRequest{ChatID: 123, Message: tooLong},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "neither chat_id nor username",
			status: domain.AccountStatusRunning,
			req:    domain.SendMessageRequest{Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "both chat_id and username",
			status: domain.AccountStatusRunning,
			req:    domain.SendMessageRequest{ChatID: 123, Username: "azhar", Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "negative topic_id",
			status: domain.AccountStatusRunning,
			req:    domain.SendMessageRequest{ChatID: 123, TopicID: -1, Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "account stopped",
			status: domain.AccountStatusStopped,
			req:    domain.SendMessageRequest{ChatID: 123, Message: "hello"},
			want:   domain.ErrBotNotRunning,
		},
		{
			name:   "account waiting otp",
			status: domain.AccountStatusWaitingOTP,
			req:    domain.SendMessageRequest{Username: "@azhar", Message: "hello"},
			want:   domain.ErrBotNotRunning,
		},
		{
			name:       "account b2b disabled",
			resolveErr: domain.ErrB2BNotEnabled,
			req:        domain.SendMessageRequest{AccountID: 2, ChatID: 123, Message: "hello"},
			want:       domain.ErrB2BNotEnabled,
		},
		{
			name:       "account not found",
			resolveErr: domain.ErrAccountNotFound,
			req:        domain.SendMessageRequest{AccountID: 99, ChatID: 123, Message: "hello"},
			want:       domain.ErrAccountNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &stubMultiAccountSender{
				resolvedAccountID: 1,
				resolvedStatus:    tc.status,
				resolveErr:        tc.resolveErr,
			}
			uc := NewMessageUseCase(sender, zap.NewNop())

			err := uc.SendMessage(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err = %v, want %v", err, tc.want)
			}
			if sender.called {
				t.Fatal("sender should not be called on validation or account-status error")
			}
		})
	}
}

func TestMessageUseCase_HappyPaths(t *testing.T) {
	t.Run("default B2B account with chat_id and topic_id", func(t *testing.T) {
		sender := &stubMultiAccountSender{resolvedAccountID: 1, resolvedStatus: domain.AccountStatusRunning}
		uc := NewMessageUseCase(sender, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			ChatID:  -1001234567890,
			TopicID: 42,
			Message: "  alert body  ",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sender.called || sender.accountID != 1 || sender.chatID != -1001234567890 || sender.topicID != 42 || sender.text != "alert body" {
			t.Fatalf("unexpected sender call: %+v", sender)
		}
	})

	t.Run("explicit account_id with username", func(t *testing.T) {
		sender := &stubMultiAccountSender{resolvedAccountID: 1, resolvedStatus: domain.AccountStatusRunning}
		uc := NewMessageUseCase(sender, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			AccountID: 3,
			Username:  " @azhar ",
			Message:   "alert dm",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sender.called || sender.accountID != 3 || sender.username != "@azhar" || sender.text != "alert dm" {
			t.Fatalf("unexpected sender call: %+v", sender)
		}
	})

	t.Run("wraps sender error", func(t *testing.T) {
		sender := &stubMultiAccountSender{resolvedAccountID: 1, resolvedStatus: domain.AccountStatusRunning, sendErr: domain.ErrTargetNotFound}
		uc := NewMessageUseCase(sender, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			ChatID:  999,
			Message: "alert",
		})
		if !errors.Is(err, domain.ErrTargetNotFound) {
			t.Fatalf("got err = %v, want ErrTargetNotFound", err)
		}
	})
}
