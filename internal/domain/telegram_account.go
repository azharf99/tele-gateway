// internal/domain/telegram_account.go
package domain

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type AccountStatus string

const (
	AccountStatusStopped    AccountStatus = "STOPPED"
	AccountStatusConnecting AccountStatus = "CONNECTING"
	AccountStatusWaitingOTP AccountStatus = "WAITING_OTP"
	AccountStatusWaiting2FA AccountStatus = "WAITING_2FA"
	AccountStatusRunning    AccountStatus = "RUNNING"
	AccountStatusError      AccountStatus = "ERROR"
)

type TelegramAccount struct {
	ID                 uint           `gorm:"primaryKey" json:"id"`
	Name               string         `gorm:"type:varchar(100);not null" json:"name"`
	PhoneNumber        string         `gorm:"type:varchar(30);uniqueIndex;not null" json:"phone_number"`
	AppID              int            `gorm:"default:0" json:"app_id"`
	AppHash            string         `gorm:"type:text" json:"-"`
	TwoFAPassword      string         `gorm:"type:text" json:"-"`
	SessionFile        string         `gorm:"type:varchar(255);not null" json:"session_file"`
	IsEnabled          bool           `gorm:"default:true" json:"is_enabled"`
	EnableB2B          bool           `gorm:"default:false" json:"enable_b2b"`
	IsDefaultB2B       bool           `gorm:"default:false" json:"is_default_b2b"`
	EnableAIGateway    bool           `gorm:"default:false" json:"enable_ai_gateway"`
	AISystemPrompt     string         `gorm:"type:text" json:"ai_system_prompt"`
	EnableClassAuction bool           `gorm:"default:false" json:"enable_class_auction"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
	DeletedAt          gorm.DeletedAt `gorm:"index" json:"-"`

	// Runtime fields (populated by AccountManager, not persisted in DB)
	Status           AccountStatus `gorm:"-" json:"status"`
	LastError        string        `gorm:"-" json:"last_error,omitempty"`
	TelegramUserID   int64         `gorm:"-" json:"telegram_user_id,omitempty"`
	TelegramUsername string        `gorm:"-" json:"telegram_username,omitempty"`
	HasTwoFAPassword bool          `gorm:"-" json:"has_2fa_password"`
	HasCustomAppHash bool          `gorm:"-" json:"has_custom_app_hash"`
}

type AccountRuntimeState struct {
	Status           AccountStatus `json:"status"`
	LastError        string        `json:"last_error,omitempty"`
	TelegramUserID   int64         `json:"telegram_user_id,omitempty"`
	TelegramUsername string        `json:"telegram_username,omitempty"`
}

type TelegramAccountRepository interface {
	Create(account *TelegramAccount) error
	Update(account *TelegramAccount) error
	Delete(id uint) error
	FindByID(id uint) (*TelegramAccount, error)
	FindByPhone(phone string) (*TelegramAccount, error)
	FindAll() ([]TelegramAccount, error)
	Count() (int64, error)
	SetDefaultB2B(id uint) error
	ClearDefaultB2BExcept(id uint) error
}

type CreateAccountInput struct {
	Name               string `json:"name" binding:"required"`
	PhoneNumber        string `json:"phone_number" binding:"required"`
	AppID              int    `json:"app_id"`
	AppHash            string `json:"app_hash"`
	TwoFAPassword      string `json:"two_fa_password"`
	IsEnabled          *bool  `json:"is_enabled"`
	EnableB2B          bool   `json:"enable_b2b"`
	IsDefaultB2B       bool   `json:"is_default_b2b"`
	EnableAIGateway    bool   `json:"enable_ai_gateway"`
	AISystemPrompt     string `json:"ai_system_prompt"`
	EnableClassAuction bool   `json:"enable_class_auction"`
}

type UpdateAccountInput struct {
	Name               *string `json:"name"`
	PhoneNumber        *string `json:"phone_number"`
	AppID              *int    `json:"app_id"`
	AppHash            *string `json:"app_hash"`
	TwoFAPassword      *string `json:"two_fa_password"`
	ClearTwoFAPassword bool    `json:"clear_2fa_password"`
	ClearCustomAppHash bool    `json:"clear_custom_app_hash"`
	IsEnabled          *bool   `json:"is_enabled"`
	EnableB2B          *bool   `json:"enable_b2b"`
	IsDefaultB2B       *bool   `json:"is_default_b2b"`
	EnableAIGateway    *bool   `json:"enable_ai_gateway"`
	AISystemPrompt     *string `json:"ai_system_prompt"`
	EnableClassAuction *bool   `json:"enable_class_auction"`
}

type AccountManager interface {
	StartAllEnabled(ctx context.Context) error
	StartAccount(accountID uint) error
	StopAccount(accountID uint) error
	RestartAccount(accountID uint) error
	SubmitOTP(accountID uint, code string) error
	Submit2FA(accountID uint, password string) error
	GetRuntimeState(accountID uint) AccountRuntimeState
	RefreshAccountRuntime(account *TelegramAccount)
	RemoveAccountRuntime(accountID uint)
	SyncGroups(ctx context.Context, accountID uint) ([]TelegramGroup, error)
	GetTopics(ctx context.Context, accountID uint, groupID int64) ([]TopicInfo, error)
	Shutdown()
}
