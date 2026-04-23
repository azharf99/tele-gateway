// internal/domain/telegram_group.go
package domain

import (
	"time"

	"gorm.io/gorm"
)

type TelegramGroup struct {
	ID        int64          `gorm:"primaryKey;autoIncrement:false" json:"id"`
	ParentID  int64          `gorm:"index" json:"parent_id"` // Jika ini Topic, ini ID Supergroupnya
	Title     string         `gorm:"type:varchar(255);not null" json:"title"`
	Type      string         `gorm:"type:varchar(50)" json:"type"` // "group", "supergroup", "channel", "topic"
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

type TelegramGroupRepository interface {
	Upsert(group *TelegramGroup) error
	FindAll() ([]TelegramGroup, error)
}

type GroupInfo struct {
	ID    int64
	Title string
	Type  string
}

type TopicInfo struct {
	ID    int `json:"id"`
	Title string `json:"title"`
}
