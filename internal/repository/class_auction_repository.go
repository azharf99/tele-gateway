// internal/repository/class_auction_repository.go
package repository

import (
	"errors"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/parser"
	"gorm.io/gorm"
)

type classAuctionRepository struct {
	db *gorm.DB
}

func NewClassAuctionRepository(db *gorm.DB) domain.ClassAuctionRepository {
	return &classAuctionRepository{db: db}
}

func (r *classAuctionRepository) GetOrCreateProfile(accountID uint) (*domain.ClassAuctionProfile, error) {
	var profile domain.ClassAuctionProfile
	err := r.db.Where("account_id = ?", accountID).First(&profile).Error
	if err == nil {
		return &profile, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	def := domain.DefaultClassAuctionProfile(accountID)
	if err := r.db.Create(def).Error; err != nil {
		// Handle concurrent creation race cleanly
		if retryErr := r.db.Where("account_id = ?", accountID).First(&profile).Error; retryErr == nil {
			return &profile, nil
		}
		return nil, err
	}
	return def, nil
}

func (r *classAuctionRepository) SaveProfile(profile *domain.ClassAuctionProfile) error {
	profile.MarshalJSONFields()
	return r.db.Save(profile).Error
}

func (r *classAuctionRepository) ListWatchedTargets(accountID uint) ([]domain.WatchedTarget, error) {
	var targets []domain.WatchedTarget
	err := r.db.Where("account_id = ?", accountID).Order("id asc").Find(&targets).Error
	return targets, err
}

func (r *classAuctionRepository) CreateWatchedTarget(target *domain.WatchedTarget) error {
	var existing domain.WatchedTarget
	err := r.db.Where("account_id = ? AND group_id = ? AND topic_id = ?",
		target.AccountID, target.GroupID, target.TopicID).First(&existing).Error
	if err == nil {
		return domain.ErrTargetAlreadyExists
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return r.db.Create(target).Error
}

func (r *classAuctionRepository) UpdateWatchedTarget(target *domain.WatchedTarget) error {
	return r.db.Save(target).Error
}

func (r *classAuctionRepository) DeleteWatchedTarget(accountID uint, targetID uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, targetID).Delete(&domain.WatchedTarget{}).Error
}

func (r *classAuctionRepository) ListWeeklyAvailability(accountID uint) ([]domain.WeeklyAvailability, error) {
	var windows []domain.WeeklyAvailability
	err := r.db.Where("account_id = ?", accountID).Order("day_of_week asc, start_minute asc").Find(&windows).Error
	return windows, err
}

func (r *classAuctionRepository) ReplaceWeeklyAvailability(accountID uint, windows []domain.WeeklyAvailability) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ?", accountID).Delete(&domain.WeeklyAvailability{}).Error; err != nil {
			return err
		}
		if len(windows) == 0 {
			return nil
		}
		for i := range windows {
			windows[i].ID = 0
			windows[i].AccountID = accountID
		}
		return tx.Create(&windows).Error
	})
}

func (r *classAuctionRepository) ListDateOverrides(accountID uint) ([]domain.DateAvailabilityOverride, error) {
	var overrides []domain.DateAvailabilityOverride
	err := r.db.Where("account_id = ?", accountID).Order("date asc, start_minute asc").Find(&overrides).Error
	return overrides, err
}

func (r *classAuctionRepository) CreateDateOverride(override *domain.DateAvailabilityOverride) error {
	return r.db.Create(override).Error
}

func (r *classAuctionRepository) DeleteDateOverride(accountID uint, overrideID uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, overrideID).Delete(&domain.DateAvailabilityOverride{}).Error
}

func (r *classAuctionRepository) CreateAuctionLog(logEntry *domain.AuctionLog) error {
	return r.db.Create(logEntry).Error
}

func (r *classAuctionRepository) FindAuctionLogByID(accountID uint, logID uint) (*domain.AuctionLog, error) {
	var entry domain.AuctionLog
	err := r.db.Where("account_id = ? AND id = ?", accountID, logID).First(&entry).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrAuctionLogNotFound
		}
		return nil, err
	}
	return &entry, nil
}

func (r *classAuctionRepository) ListAuctionLogs(accountID uint, filter domain.AuctionLogFilter) (*domain.AuctionLogPage, error) {
	q := r.db.Model(&domain.AuctionLog{}).Where("account_id = ?", accountID)
	if filter.Decision != "" {
		q = q.Where("decision = ?", filter.Decision)
	}
	if filter.Outcome != "" {
		q = q.Where("outcome = ?", filter.Outcome)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}

	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	var items []domain.AuctionLog
	if err := q.Order("created_at desc, id desc").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, err
	}
	return &domain.AuctionLogPage{
		Items: items,
		Total: total,
	}, nil
}

func (r *classAuctionRepository) UpdateAuctionLogOutcome(accountID uint, logID uint, outcome domain.AuctionOutcome) error {
	res := r.db.Model(&domain.AuctionLog{}).
		Where("account_id = ? AND id = ?", accountID, logID).
		Update("outcome", outcome)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.ErrAuctionLogNotFound
	}
	return nil
}

func (r *classAuctionRepository) GetStats(accountID uint) (*domain.AuctionStats, error) {
	stats := &domain.AuctionStats{}

	base := r.db.Model(&domain.AuctionLog{})
	if accountID > 0 {
		base = base.Where("account_id = ?", accountID)
	}

	if err := base.Session(&gorm.Session{}).Where("decision = ?", domain.DecisionBidSent).Count(&stats.TotalBidsSent).Error; err != nil {
		return nil, err
	}
	if err := base.Session(&gorm.Session{}).Where("decision = ?", domain.DecisionDryRunMatch).Count(&stats.TotalDryRun).Error; err != nil {
		return nil, err
	}
	if err := base.Session(&gorm.Session{}).Where("decision = ?", domain.DecisionSkipped).Count(&stats.TotalSkipped).Error; err != nil {
		return nil, err
	}
	if err := base.Session(&gorm.Session{}).Where("outcome = ?", domain.OutcomeWon).Count(&stats.TotalWon).Error; err != nil {
		return nil, err
	}

	nowWIB := time.Now().In(parser.WIBLocation())
	startOfDay := time.Date(nowWIB.Year(), nowWIB.Month(), nowWIB.Day(), 0, 0, 0, 0, parser.WIBLocation())
	if err := base.Session(&gorm.Session{}).Where("decision = ? AND created_at >= ?", domain.DecisionBidSent, startOfDay).Count(&stats.BidsToday).Error; err != nil {
		return nil, err
	}

	var avg struct {
		Avg float64
	}
	_ = base.Session(&gorm.Session{}).
		Select("COALESCE(AVG(latency_ms), 0) as avg").
		Where("decision = ?", domain.DecisionBidSent).
		Scan(&avg).Error
	stats.AvgLatencyMs = int64(avg.Avg)

	return stats, nil
}

func (r *classAuctionRepository) DeleteByAccountID(accountID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ?", accountID).Delete(&domain.AuctionLog{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&domain.DateAvailabilityOverride{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&domain.WeeklyAvailability{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&domain.WatchedTarget{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ?", accountID).Delete(&domain.ClassAuctionProfile{}).Error
	})
}
