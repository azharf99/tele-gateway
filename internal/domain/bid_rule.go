// internal/domain/bid_rule.go
package domain

import (
	"context"

	"github.com/gotd/td/tg"
	"gorm.io/gorm"
)

type BidRule struct {
	gorm.Model
	TargetGroupID int64  `gorm:"index" json:"target_group_id"`       // ID Grup Lelang
	TopicID       int    `gorm:"default:0" json:"topic_id"`          // ID Topic (Forum Thread) jika ada. 0 berarti topic umum.
	Keyword       string `gorm:"uniqueIndex" json:"keyword"`         // Contoh: "iPhone 13 Pro"
	BidMessage    string `json:"bid_message"`                        // Pesan bid: "OB", "Bid 500k", dll
	IsActive      bool   `gorm:"default:true" json:"is_active"`      // Bisa dimatikan manual via DB
	HasBidded     bool   `gorm:"default:false" json:"has_bidded"`    // Mencegah spam bid berkali-kali
	StopKeywords  string `json:"stop_keywords"`                      // Contoh: "Sold", "Closed" (Separated by comma)
}

type BidRepository interface {
	Create(rule *BidRule) error
	Update(rule *BidRule) error
	Delete(id uint) error
	FindByID(id uint) (*BidRule, error)
	FindAll() ([]BidRule, error)
	GetActiveRuleByKeyword(keyword string, groupID int64, topicID int) (*BidRule, error) // Added groupID and topicID
	MarkAsBidded(id uint) error
	DeactivateRule(id uint) error
	CheckStopKeyword(id uint, text string) (bool, error)
	GetActiveRulesByGroup(groupID int64, topicID int) ([]BidRule, error)
}

type AuctionUseCase interface {
	CheckKeyword(text string, groupID int64, topicID int) (*BidRule, error)                   // Added groupID and topicID
	ExecuteBid(ctx context.Context, peer tg.InputPeerClass, msgID int, rule *BidRule) error // Changed topicID to msgID
	CheckAndStop(ctx context.Context, text string, ruleID uint) error
	CheckAndStopByText(ctx context.Context, text string, groupID int64, topicID int) error

	// API Methods
	CreateRule(rule *BidRule) error
	UpdateRule(rule *BidRule) error
	DeleteRule(id uint) error
	GetAllRules() ([]BidRule, error)
	SubmitOTP(code string) error
	GetStatus() string // "WAITING_OTP", "RUNNING", "IDLE"
	SetStatus(status string)

	// Groups Management
	SyncGroups(ctx context.Context) error
	GetAllGroups() ([]TelegramGroup, error)
	GetTopicsByGroup(ctx context.Context, groupID int64) ([]TopicInfo, error)
}
