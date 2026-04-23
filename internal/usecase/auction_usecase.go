// internal/usecase/auction_usecase.go
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type TelegramService interface {
	Reply(ctx context.Context, peer tg.InputPeerClass, msgID int, message string) error
	GetGroups(ctx context.Context) ([]domain.GroupInfo, error)
	GetTopics(ctx context.Context, groupID int64) ([]domain.TopicInfo, error)
}

type auctionUseCase struct {
	Repo      domain.BidRepository
	GroupRepo domain.TelegramGroupRepository
	TgClient  TelegramService
	Logger    *zap.Logger
	otpChan   chan string
	status    string // WAITING_OTP, RUNNING, IDLE
}

func NewAuctionUseCase(repo domain.BidRepository, groupRepo domain.TelegramGroupRepository, tgClient TelegramService, logger *zap.Logger) domain.AuctionUseCase {
	return &auctionUseCase{
		Repo:      repo,
		GroupRepo: groupRepo,
		TgClient:  tgClient,
		Logger:    logger,
		otpChan:   make(chan string, 1),
		status:    "IDLE",
	}
}

func (u *auctionUseCase) CheckKeyword(text string, groupID int64, topicID int) (*domain.BidRule, error) {
	return u.Repo.GetActiveRuleByKeyword(text, groupID, topicID)
}

func (u *auctionUseCase) ExecuteBid(ctx context.Context, peer tg.InputPeerClass, msgID int, rule *domain.BidRule) error {
	// Re-fetch the rule from DB to prevent race conditions during delay
	latestRule, err := u.Repo.FindByID(rule.ID)
	if err != nil {
		return err
	}

	if latestRule.HasBidded || !latestRule.IsActive {
		u.Logger.Info("Bid cancelled because rule is no longer active or already bidded", zap.Uint("rule_id", rule.ID))
		return nil
	}

	u.Logger.Info("Sending bid message...", zap.String("keyword", latestRule.Keyword), zap.String("bid_message", latestRule.BidMessage), zap.Int("msg_id", msgID))
	err = u.TgClient.Reply(ctx, peer, msgID, latestRule.BidMessage)
	if err != nil {
		return err
	}

	return u.Repo.MarkAsBidded(latestRule.ID)
}

func (u *auctionUseCase) CheckAndStopByText(ctx context.Context, text string, groupID int64, topicID int) error {
	rules, err := u.Repo.GetActiveRulesByGroup(groupID, topicID)
	if err != nil {
		return err
	}

	for _, r := range rules {
		if r.StopKeywords != "" {
			err := u.CheckAndStop(ctx, text, r.ID)
			if err != nil {
				u.Logger.Error("Failed to check and stop rule", zap.Uint("rule_id", r.ID), zap.Error(err))
			}
		}
	}
	return nil
}

func (u *auctionUseCase) CheckAndStop(ctx context.Context, text string, ruleID uint) error {
	stop, err := u.Repo.CheckStopKeyword(ruleID, text)
	if err != nil {
		return err
	}
	if stop {
		return u.Repo.DeactivateRule(ruleID)
	}
	return nil
}

// API Methods
func (u *auctionUseCase) CreateRule(rule *domain.BidRule) error {
	return u.Repo.Create(rule)
}

func (u *auctionUseCase) UpdateRule(rule *domain.BidRule) error {
	return u.Repo.Update(rule)
}

func (u *auctionUseCase) DeleteRule(id uint) error {
	return u.Repo.Delete(id)
}

func (u *auctionUseCase) GetAllRules() ([]domain.BidRule, error) {
	return u.Repo.FindAll()
}

func (u *auctionUseCase) SubmitOTP(code string) error {
	if u.status != "WAITING_OTP" {
		return errors.New("not waiting for OTP")
	}

	// Gunakan select dengan timeout agar tidak blocking selamanya (Deadlock)
	// jika WaitOTP sudah ter-cancel atau timeout
	select {
	case u.otpChan <- code:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timeout: bot is not receiving OTP right now")
	}
}

func (u *auctionUseCase) GetStatus() string {
	return u.status
}

func (u *auctionUseCase) SetStatus(status string) {
	u.status = status
}

func (u *auctionUseCase) SyncGroups(ctx context.Context) error {
	if u.status != "RUNNING" {
		return errors.New("bot is not fully running yet. please ensure login/OTP is completed")
	}
	groups, err := u.TgClient.GetGroups(ctx)
	if err != nil {
		return err
	}

	for _, g := range groups {
		err := u.GroupRepo.Upsert(&domain.TelegramGroup{
			ID:    g.ID,
			Title: g.Title,
			Type:  g.Type,
		})
		if err != nil {
			u.Logger.Error("Failed to upsert group", zap.Int64("id", g.ID), zap.Error(err))
		}
	}
	return nil
}

func (u *auctionUseCase) GetAllGroups() ([]domain.TelegramGroup, error) {
	return u.GroupRepo.FindAll()
}

func (u *auctionUseCase) GetTopicsByGroup(ctx context.Context, groupID int64) ([]domain.TopicInfo, error) {
	if u.status != "RUNNING" {
		return nil, errors.New("bot is not fully running yet. please ensure login/OTP is completed")
	}
	return u.TgClient.GetTopics(ctx, groupID)
}

// Internal method for Gotd Auth Flow
func (u *auctionUseCase) WaitOTP(ctx context.Context) (string, error) {
	u.status = "WAITING_OTP"

	// Pastikan status di-reset jika gagal/timeout, agar tidak nyangkut di WAITING_OTP
	defer func() {
		if u.status == "WAITING_OTP" {
			u.status = "IDLE"
		}
	}()

	select {
	case code := <-u.otpChan:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", errors.New("OTP timeout")
	}
}
