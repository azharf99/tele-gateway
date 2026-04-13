// internal/repository/telegram_group_repository.go
package repository

import (
	"github.com/azharf99/tele-gateway/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type telegramGroupRepository struct {
	db *gorm.DB
}

func NewTelegramGroupRepository(db *gorm.DB) domain.TelegramGroupRepository {
	return &telegramGroupRepository{db: db}
}

func (r *telegramGroupRepository) Upsert(group *domain.TelegramGroup) error {
	return r.db.Clauses(clause.OnConflict{
		UpdateAll: true,
	}).Create(group).Error
}

func (r *telegramGroupRepository) FindAll() ([]domain.TelegramGroup, error) {
	var groups []domain.TelegramGroup
	err := r.db.Order("title asc").Find(&groups).Error
	return groups, err
}
