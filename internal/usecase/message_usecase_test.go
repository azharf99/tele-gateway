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

type stubMessageSender struct {
	called   bool
	chatID   int64
	topicID  int
	username string
	text     string
	err      error
}

func (s *stubMessageSender) SendMessage(_ context.Context, chatID int64, topicID int, username string, text string) error {
	s.called = true
	s.chatID = chatID
	s.topicID = topicID
	s.username = username
	s.text = text
	return s.err
}

type stubStatusProvider struct {
	status string
}

func (s stubStatusProvider) GetStatus() string {
	return s.status
}

func TestMessageUseCase_Validation(t *testing.T) {
	tooLong := strings.Repeat("a", maxTelegramMessageRunes+1)
	tests := []struct {
		name   string
		status string
		req    domain.SendMessageRequest
		want   error
	}{
		{
			name:   "empty message",
			status: "RUNNING",
			req:    domain.SendMessageRequest{ChatID: 123, Message: "   "},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "message exceeds max runes",
			status: "RUNNING",
			req:    domain.SendMessageRequest{ChatID: 123, Message: tooLong},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "neither chat_id nor username",
			status: "RUNNING",
			req:    domain.SendMessageRequest{Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "both chat_id and username",
			status: "RUNNING",
			req:    domain.SendMessageRequest{ChatID: 123, Username: "azhar", Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "negative topic_id",
			status: "RUNNING",
			req:    domain.SendMessageRequest{ChatID: 123, TopicID: -1, Message: "hello"},
			want:   domain.ErrInvalidTarget,
		},
		{
			name:   "bot idle",
			status: "IDLE",
			req:    domain.SendMessageRequest{ChatID: 123, Message: "hello"},
			want:   domain.ErrBotNotRunning,
		},
		{
			name:   "bot waiting otp",
			status: "WAITING_OTP",
			req:    domain.SendMessageRequest{Username: "@azhar", Message: "hello"},
			want:   domain.ErrBotNotRunning,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender := &stubMessageSender{}
			uc := NewMessageUseCase(sender, stubStatusProvider{status: tc.status}, zap.NewNop())

			err := uc.SendMessage(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err = %v, want %v", err, tc.want)
			}
			if sender.called {
				t.Fatal("sender should not be called on validation or bot-status error")
			}
		})
	}
}

func TestMessageUseCase_HappyPaths(t *testing.T) {
	t.Run("chat_id with topic_id", func(t *testing.T) {
		sender := &stubMessageSender{}
		uc := NewMessageUseCase(sender, stubStatusProvider{status: "RUNNING"}, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			ChatID:  -1001234567890,
			TopicID: 42,
			Message: "  alert body  ",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sender.called || sender.chatID != -1001234567890 || sender.topicID != 42 || sender.text != "alert body" {
			t.Fatalf("unexpected sender call: %+v", sender)
		}
	})

	t.Run("username", func(t *testing.T) {
		sender := &stubMessageSender{}
		uc := NewMessageUseCase(sender, stubStatusProvider{status: "RUNNING"}, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			Username: " @azhar ",
			Message:  "alert dm",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !sender.called || sender.username != "@azhar" || sender.text != "alert dm" {
			t.Fatalf("unexpected sender call: %+v", sender)
		}
	})

	t.Run("wraps sender error", func(t *testing.T) {
		sender := &stubMessageSender{err: domain.ErrTargetNotFound}
		uc := NewMessageUseCase(sender, stubStatusProvider{status: "RUNNING"}, zap.NewNop())

		err := uc.SendMessage(context.Background(), domain.SendMessageRequest{
			ChatID:  999,
			Message: "alert",
		})
		if !errors.Is(err, domain.ErrTargetNotFound) {
			t.Fatalf("got err = %v, want ErrTargetNotFound", err)
		}
	})
}
