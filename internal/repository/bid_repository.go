// internal/repository/bid_repository.go
package repository

import (
	"errors"
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

// matchPattern reports whether a single keyword pattern matches the given
// message text. The pattern is compiled as a case-insensitive regular
// expression; if it is not valid regex, it falls back to a case-insensitive
// substring check.
//
// Matching is attempted against the full text AND against each individual line
// of the message. Telegram auction posts are frequently multi-line (e.g. a
// "Course: ... / Jadwal: ... / Req: ..." block), and Go's regexp runs in
// non-multiline mode by default, so an anchored pattern like `^Senin ... WIB$`
// would never match the whole block. Checking line-by-line lets such a pattern
// match the one relevant row while still rejecting rows like
// "Jadwal: Senin 19.00WIB dan Sabtu 18.00WIB" that don't satisfy the anchors.
func matchPattern(pattern, text string) bool {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		// Not valid regex: fall back to a case-insensitive substring match.
		return strings.Contains(strings.ToLower(text), strings.ToLower(pattern))
	}

	if re.MatchString(text) {
		return true
	}

	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if re.MatchString(line) {
			return true
		}
	}

	return false
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

// DeleteMany removes every rule whose ID is in ids and reports how many rows were
// actually deleted, which can be lower than len(ids) if some were already gone.
// Like Delete this is a hard delete, so the unique keyword index is freed up
// immediately and the same keyword can be re-imported afterwards.
func (r *bidRepository) DeleteMany(ids []uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	res := r.db.Unscoped().Where("id IN ?", ids).Delete(&domain.BidRule{})
	return res.RowsAffected, res.Error
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
		allMatched := true

		for k := range strings.SplitSeq(rule.Keyword, ",") {
			pattern := strings.TrimSpace(k)
			if pattern == "" {
				continue
			}

			// All comma-separated patterns must match (logical AND). matchPattern
			// checks each line of the message individually so anchored patterns
			// still fire inside multi-line auction posts.
			if !matchPattern(pattern, keyword) {
				allMatched = false
				break
			}
		}

		if allMatched {
			matched := rule
			return &matched, nil
		}
	}

	return nil, gorm.ErrRecordNotFound
}

// ClaimForBid marks a rule as bidded, but only if it is still active and has not
// been bidded yet, and reports whether this caller is the one that won the claim.
//
// The condition lives in the UPDATE itself so the check and the write are a
// single atomic statement. Two goroutines racing on the same rule — duplicate
// deliveries of one auction post, or two posts arriving within the bid delay —
// therefore produce exactly one winner and one bid, instead of both reading
// has_bidded=false and both sending.
func (r *bidRepository) ClaimForBid(id uint) (bool, error) {
	res := r.db.Model(&domain.BidRule{}).
		Where("id = ? AND has_bidded = ? AND is_active = ?", id, false, true).
		Update("has_bidded", true)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ReleaseBid undoes a claim taken by ClaimForBid. Used when the bid could not be
// delivered, so a later matching message still gets a chance to bid.
func (r *bidRepository) ReleaseBid(id uint) error {
	return r.db.Model(&domain.BidRule{}).Where("id = ?", id).Update("has_bidded", false).Error
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

	for k := range strings.SplitSeq(rule.StopKeywords, ",") {
		pattern := strings.TrimSpace(k)
		if pattern == "" {
			continue
		}

		// Any stop keyword matching (on the full text or any single line)
		// deactivates the rule (logical OR).
		if matchPattern(pattern, text) {
			return true, nil
		}
	}
	return false, nil
}

// BulkUpsert inserts or replaces rules keyed by their unique Keyword, all inside a
// single transaction. If a rule with the same keyword already exists (including a
// soft-deleted one), every editable field is overwritten and the row is revived;
// otherwise a new row is created. Returns the number of created and updated rows.
//
// A map is used for the update set (not a struct) so that boolean false / zero
// values — e.g. is_active=false, has_bidded=false, topic_id=0 — are actually
// written; GORM silently skips zero-valued struct fields on Updates.
func (r *bidRepository) BulkUpsert(rules []domain.BidRule) (int, int, error) {
	var created, updated int

	err := r.db.Transaction(func(tx *gorm.DB) error {
		for i := range rules {
			rule := rules[i]

			var existing domain.BidRule
			lookupErr := tx.Unscoped().Where("keyword = ?", rule.Keyword).First(&existing).Error

			switch {
			case lookupErr == nil:
				if err := tx.Unscoped().Model(&domain.BidRule{}).Where("id = ?", existing.ID).
					Updates(map[string]any{
						"target_group_id": rule.TargetGroupID,
						"topic_id":        rule.TopicID,
						"keyword":         rule.Keyword,
						"bid_message":     rule.BidMessage,
						"stop_keywords":   rule.StopKeywords,
						"is_active":       rule.IsActive,
						"has_bidded":      rule.HasBidded,
						"deleted_at":      nil, // revive if it was soft-deleted
					}).Error; err != nil {
					return err
				}
				updated++

			case errors.Is(lookupErr, gorm.ErrRecordNotFound):
				if err := tx.Create(&rule).Error; err != nil {
					return err
				}
				created++

			default:
				return lookupErr
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	return created, updated, nil
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
