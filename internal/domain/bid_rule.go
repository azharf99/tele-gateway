// internal/domain/bid_rule.go
package domain

import (
	"context"

	"github.com/gotd/td/tg"
	"gorm.io/gorm"
)

type BidRule struct {
	gorm.Model
	TargetGroupID int64  `gorm:"index"`       // ID Grup Lelang
	Keyword       string `gorm:"uniqueIndex"` // Contoh: "iPhone 13 Pro"
	BidMessage    string // Pesan bid: "OB", "Bid 500k", dll
	IsActive      bool   `gorm:"default:true"`  // Bisa dimatikan manual via DB
	HasBidded     bool   `gorm:"default:false"` // Mencegah spam bid berkali-kali
	StopKeywords  string // Contoh: "Sold", "Closed" (Separated by comma)
}

type BidRepository interface {
	Create(rule *BidRule) error
	Update(rule *BidRule) error
	Delete(id uint) error
	FindByID(id uint) (*BidRule, error)
	FindAll() ([]BidRule, error)
	GetActiveRuleByKeyword(keyword string) (*BidRule, error)
	MarkAsBidded(id uint) error
	DeactivateRule(id uint) error
	CheckStopKeyword(id uint, text string) (bool, error)
}

type AuctionUseCase interface {
	CheckKeyword(text string) (*BidRule, error)
	ExecuteBid(ctx context.Context, peer tg.InputPeerClass, rule *BidRule) error
	CheckAndStop(ctx context.Context, text string, ruleID uint) error
	
	// API Methods
	CreateRule(rule *BidRule) error
	UpdateRule(rule *BidRule) error
	DeleteRule(id uint) error
	GetAllRules() ([]BidRule, error)
	SubmitOTP(code string) error
	GetStatus() string // "WAITING_OTP", "RUNNING", "IDLE"

	// Groups Management
	SyncGroups(ctx context.Context) error
	GetAllGroups() ([]TelegramGroup, error)
}
