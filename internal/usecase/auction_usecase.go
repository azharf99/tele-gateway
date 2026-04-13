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
	Reply(ctx context.Context, peer tg.InputPeerClass, message string) error
	GetGroups(ctx context.Context) ([]domain.GroupInfo, error)
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
		otpChan:   make(chan string),
		status:    "IDLE",
	}
}

func (u *auctionUseCase) CheckKeyword(text string) (*domain.BidRule, error) {
	return u.Repo.GetActiveRuleByKeyword(text)
}

func (u *auctionUseCase) ExecuteBid(ctx context.Context, peer tg.InputPeerClass, rule *domain.BidRule) error {
	if rule.HasBidded || !rule.IsActive {
		return nil
	}

	u.Logger.Info("Sending bid message...", zap.String("keyword", rule.Keyword), zap.String("bid_message", rule.BidMessage))
	err := u.TgClient.Reply(ctx, peer, rule.BidMessage)
	if err != nil {
		return err
	}

	return u.Repo.MarkAsBidded(rule.ID)
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
	u.otpChan <- code
	return nil
}

func (u *auctionUseCase) GetStatus() string {
	return u.status
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

// Internal method for Gotd Auth Flow
func (u *auctionUseCase) WaitOTP(ctx context.Context) (string, error) {
	u.status = "WAITING_OTP"
	defer func() { u.status = "RUNNING" }()
	
	select {
	case code := <-u.otpChan:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", errors.New("OTP timeout")
	}
}
