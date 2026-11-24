// internal/domain/message.go
package domain

import (
	"context"
	"errors"
)

var (
	ErrInvalidTarget  = errors.New("invalid message target or content")
	ErrBotNotRunning  = errors.New("telegram userbot is not running")
	ErrTargetNotFound = errors.New("telegram target not found")
)

type SendMessageRequest struct {
	ChatID   int64  `json:"chat_id,omitempty"`
	TopicID  int    `json:"topic_id,omitempty"`
	Username string `json:"username,omitempty"`
	Message  string `json:"message" binding:"required"`
}

type MessageUseCase interface {
	SendMessage(ctx context.Context, req SendMessageRequest) error
}
