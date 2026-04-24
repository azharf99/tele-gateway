// internal/repository/bid_repository.go
package repository

import (
	"regexp"
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
	var existing domain.BidRule
	err := r.db.Unscoped().Where("keyword = ?", rule.Keyword).First(&existing).Error
	if err == nil && existing.DeletedAt.Valid {
		// Recover the soft-deleted record
		rule.ID = existing.ID
		rule.CreatedAt = existing.CreatedAt
		// We use Save to update all fields and clear DeletedAt
		return r.db.Unscoped().Model(rule).Select("*").Updates(rule).Update("deleted_at", nil).Error
	}
	return r.db.Create(rule).Error
}

func (r *bidRepository) Update(rule *domain.BidRule) error {
	return r.db.Save(rule).Error
}

func (r *bidRepository) Delete(id uint) error {
	return r.db.Unscoped().Delete(&domain.BidRule{}, id).Error
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

	for _, rule := range rules {
		ruleKeywords := strings.Split(rule.Keyword, ",")
		allMatched := true

		for _, k := range ruleKeywords {
			pattern := strings.TrimSpace(k)
			if pattern == "" {
				continue
			}

			// Try to compile as regex (case-insensitive)
			re, err := regexp.Compile("(?i)" + pattern)
			if err == nil {
				if !re.MatchString(keyword) {
					allMatched = false
					break
				}
			} else {
				// Fallback to simple substring
				if !strings.Contains(strings.ToLower(keyword), strings.ToLower(pattern)) {
					allMatched = false
					break
				}
			}
		}

		if allMatched {
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
	for _, k := range keywords {
		pattern := strings.TrimSpace(k)
		if pattern == "" {
			continue
		}

		re, err := regexp.Compile("(?i)" + pattern)
		if err == nil {
			if re.MatchString(text) {
				return true, nil
			}
		} else {
			if strings.Contains(strings.ToLower(text), strings.ToLower(pattern)) {
				return true, nil
			}
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
