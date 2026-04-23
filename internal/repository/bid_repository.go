// internal/repository/bid_repository.go
package repository

import (
	"strings"

	"github.com/azharf99/tele-gateway/internal/domain"
	"gorm.io/gorm"
)

type bidRepository struct {
	db *gorm.DB
}

func NewBidRepository(db *gorm.DB) domain.BidRepository {
	return &bidRepository{db: db}
}

func (r *bidRepository) Create(rule *domain.BidRule) error {
	return r.db.Create(rule).Error
}

func (r *bidRepository) Update(rule *domain.BidRule) error {
	return r.db.Save(rule).Error
}

func (r *bidRepository) Delete(id uint) error {
	return r.db.Delete(&domain.BidRule{}, id).Error
}

func (r *bidRepository) FindByID(id uint) (*domain.BidRule, error) {
	var rule domain.BidRule
	err := r.db.First(&rule, id).Error
	return &rule, err
}

func (r *bidRepository) FindAll() ([]domain.BidRule, error) {
	var rules []domain.BidRule
	err := r.db.Find(&rules).Error
	return rules, err
}

func (r *bidRepository) GetActiveRuleByKeyword(keyword string, groupID int64, topicID int) (*domain.BidRule, error) {
	var rules []domain.BidRule
	query := r.db.Where("is_active = ? AND target_group_id = ?", true, groupID)

	// topic_id=0 artinya rule global untuk group tersebut.
	if topicID > 0 {
		query = query.Where("(topic_id = ? OR topic_id = 0)", topicID)
	} else {
		query = query.Where("topic_id = 0")
	}

	if err := query.Order("topic_id desc, id asc").Find(&rules).Error; err != nil {
		return nil, err
	}

	textLower := strings.ToLower(keyword)
	for _, rule := range rules {
		if strings.Contains(textLower, strings.ToLower(strings.TrimSpace(rule.Keyword))) {
			matched := rule
			return &matched, nil
		}
	}

	return nil, gorm.ErrRecordNotFound
}

func (r *bidRepository) MarkAsBidded(id uint) error {
	return r.db.Model(&domain.BidRule{}).Where("id = ?", id).Update("has_bidded", true).Error
}

func (r *bidRepository) DeactivateRule(id uint) error {
	return r.db.Model(&domain.BidRule{}).Where("id = ?", id).Update("is_active", false).Error
}

func (r *bidRepository) CheckStopKeyword(id uint, text string) (bool, error) {
	var rule domain.BidRule
	err := r.db.Select("stop_keywords").First(&rule, id).Error
	if err != nil {
		return false, err
	}

	if rule.StopKeywords == "" {
		return false, nil
	}

	keywords := strings.Split(rule.StopKeywords, ",")
	textLower := strings.ToLower(text)
	for _, k := range keywords {
		if strings.Contains(textLower, strings.TrimSpace(strings.ToLower(k))) {
			return true, nil
		}
	}
	return false, nil
}

func (r *bidRepository) GetActiveRulesByGroup(groupID int64, topicID int) ([]domain.BidRule, error) {
	var rules []domain.BidRule
	query := r.db.Where("is_active = ? AND target_group_id = ?", true, groupID)

	if topicID > 0 {
		query = query.Where("(topic_id = ? OR topic_id = 0)", topicID)
	} else {
		query = query.Where("topic_id = 0")
	}

	err := query.Find(&rules).Error
	return rules, err
}
