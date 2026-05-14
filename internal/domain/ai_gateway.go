// internal/domain/ai_gateway.go
package domain

import (
	"context"
	"gorm.io/gorm"
)

type AIContext struct {
	gorm.Model
	Key      string `gorm:"uniqueIndex" json:"key"`      // e.g., "system_prompt"
	Value    string `json:"value"`                       // The actual instruction
	IsActive bool   `gorm:"default:true" json:"is_active"`
}

type AIGatewayUseCase interface {
	HandlePrivateMessage(ctx context.Context, senderID int64, senderName string, text string, replyFunc func(string) error) error
	
	// API Methods for context management
	SetAIContext(key, value string) error
	GetAIContext(key string) (string, error)
}

type AIContextRepository interface {
	Set(ctx *AIContext) error
	Get(key string) (*AIContext, error)
}
