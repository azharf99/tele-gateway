// internal/repository/ai_context_repository.go
package repository

import (
	"github.com/azharf99/tele-gateway/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type aiContextRepository struct {
	db *gorm.DB
}

func NewAIContextRepository(db *gorm.DB) domain.AIContextRepository {
	return &aiContextRepository{db: db}
}

func (r *aiContextRepository) Set(ctx *domain.AIContext) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "is_active"}),
	}).Create(ctx).Error
}

func (r *aiContextRepository) Get(key string) (*domain.AIContext, error) {
	var ctx domain.AIContext
	err := r.db.Where("key = ? AND is_active = ?", key, true).First(&ctx).Error
	if err != nil {
		return nil, err
	}
	return &ctx, nil
}
