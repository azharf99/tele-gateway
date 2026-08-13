// internal/domain/bid_rule.go
package domain

import (
	"context"
	"errors"

	"github.com/gotd/td/tg"
	"gorm.io/gorm"
)

// ErrInvalidRuleSelection means a bulk operation was handed an unusable set of
// rule IDs (empty, or more than the server is willing to process at once).
// Handlers translate it to 400 rather than 500.
var ErrInvalidRuleSelection = errors.New("invalid rule selection")

// BidRule is looked up on every single incoming group message, so the
// group/topic/active columns carry a composite index (idx_bid_rules_lookup) that
// matches the WHERE clause of GetActiveRuleByKeyword and GetActiveRulesByGroup.
type BidRule struct {
	gorm.Model
	TargetGroupID int64  `gorm:"index;index:idx_bid_rules_lookup,priority:1" json:"target_group_id"`  // ID Grup Lelang
	TopicID       int    `gorm:"default:0;index:idx_bid_rules_lookup,priority:2" json:"topic_id"`     // ID Topic (Forum Thread) jika ada. 0 berarti topic umum.
	Keyword       string `gorm:"uniqueIndex" json:"keyword"`                                          // Contoh: "iPhone 13 Pro"
	BidMessage    string `json:"bid_message"`                                                         // Pesan bid: "OB", "Bid 500k", dll
	IsActive      bool   `gorm:"default:true;index:idx_bid_rules_lookup,priority:3" json:"is_active"` // Bisa dimatikan manual via DB
	HasBidded     bool   `gorm:"default:false" json:"has_bidded"`                                     // Mencegah spam bid berkali-kali
	StopKeywords  string `json:"stop_keywords"`                                                       // Contoh: "Sold", "Closed" (Separated by comma)
}

type BidRepository interface {
	Create(rule *BidRule) error
	Update(rule *BidRule) error
	Delete(id uint) error
	DeleteMany(ids []uint) (deleted int64, err error) // Bulk delete by ID
	FindByID(id uint) (*BidRule, error)
	FindAll() ([]BidRule, error)
	GetActiveRuleByKeyword(keyword string, groupID int64, topicID int) (*BidRule, error) // Added groupID and topicID
	ClaimForBid(id uint) (claimed bool, err error)                                       // Atomic guard against double-bidding
	ReleaseBid(id uint) error                                                            // Undo a claim when the send failed
	DeactivateRule(id uint) error
	CheckStopKeyword(id uint, text string) (bool, error)
	GetActiveRulesByGroup(groupID int64, topicID int) ([]BidRule, error)
	BulkUpsert(rules []BidRule) (created int, updated int, err error) // Upsert by unique keyword (revives soft-deleted)
}

type AuctionUseCase interface {
	CheckKeyword(text string, groupID int64, topicID int) (*BidRule, error)                 // Added groupID and topicID
	ExecuteBid(ctx context.Context, peer tg.InputPeerClass, msgID int, rule *BidRule) error // Changed topicID to msgID
	CheckAndStop(ctx context.Context, text string, ruleID uint) error
	CheckAndStopByText(ctx context.Context, text string, groupID int64, topicID int) error

	// API Methods
	CreateRule(rule *BidRule) error
	UpdateRule(rule *BidRule) error
	DeleteRule(id uint) error
	DeleteRules(ids []uint) (deleted int64, err error) // Bulk delete; wraps ErrInvalidRuleSelection on bad input
	GetAllRules() ([]BidRule, error)
	ImportRules(rules []BidRule) (created int, updated int, err error) // Bulk CSV import via upsert
	SubmitOTP(code string) error
	GetStatus() string // "WAITING_OTP", "RUNNING", "IDLE"
	SetStatus(status string)

	// Groups Management
	SyncGroups(ctx context.Context) error
	GetAllGroups() ([]TelegramGroup, error)
	GetTopicsByGroup(ctx context.Context, groupID int64) ([]TopicInfo, error)
	ReplyToUser(ctx context.Context, peer tg.InputPeerClass, msgID int, message string) error
}
