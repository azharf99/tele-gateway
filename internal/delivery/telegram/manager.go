// internal/delivery/telegram/manager.go
package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/config"
	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const authWaitTimeout = 5 * time.Minute

type accountRuntime struct {
	account      domain.TelegramAccount
	client       *TelegramClient
	handler      *AccountMessageHandler
	cancel       context.CancelFunc
	done         chan struct{}
	otpChan      chan string
	passwordChan chan string
	state        domain.AccountRuntimeState
}

// AccountManager supervises multiple concurrent gotd MTProto userbot clients,
// bridging them cleanly to B2B messaging, Watchdog alerts, AI DM gateway, and Class Auction.
type AccountManager struct {
	cfg         *config.Config
	accountRepo domain.TelegramAccountRepository
	groupRepo   domain.TelegramGroupRepository
	auctionUC   domain.ClassAuctionUseCase
	aiUC        domain.AIGatewayUseCase
	logger      *zap.Logger

	parentCtx context.Context
	mu        sync.RWMutex
	runtimes  map[uint]*accountRuntime
	wg        sync.WaitGroup
}

func NewAccountManager(
	cfg *config.Config,
	accountRepo domain.TelegramAccountRepository,
	groupRepo domain.TelegramGroupRepository,
	auctionUC domain.ClassAuctionUseCase,
	aiUC domain.AIGatewayUseCase,
	logger *zap.Logger,
) *AccountManager {
	return &AccountManager{
		cfg:         cfg,
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		auctionUC:   auctionUC,
		aiUC:        aiUC,
		logger:      logger,
		parentCtx:   context.Background(),
		runtimes:    make(map[uint]*accountRuntime),
	}
}

func (m *AccountManager) StartAllEnabled(ctx context.Context) error {
	m.mu.Lock()
	m.parentCtx = ctx
	m.mu.Unlock()

	accounts, err := m.accountRepo.FindAll()
	if err != nil {
		return fmt.Errorf("load telegram accounts: %w", err)
	}

	for i := range accounts {
		acc := accounts[i]
		m.mu.Lock()
		m.runtimes[acc.ID] = &accountRuntime{
			account: acc,
			state:   domain.AccountRuntimeState{Status: domain.AccountStatusStopped},
		}
		m.mu.Unlock()

		if acc.IsEnabled {
			if err := m.StartAccount(acc.ID); err != nil && m.logger != nil {
				m.logger.Error("Failed to start telegram account on boot",
					zap.Uint("account_id", acc.ID),
					zap.String("name", acc.Name),
					zap.Error(err))
			}
		}
	}
	return nil
}

func (m *AccountManager) StartAccount(accountID uint) error {
	acc, err := m.accountRepo.FindByID(accountID)
	if err != nil {
		return err
	}

	appID := acc.AppID
	if appID == 0 {
		appID = m.cfg.AppID
	}
	appHash := strings.TrimSpace(acc.AppHash)
	if appHash == "" {
		appHash = strings.TrimSpace(m.cfg.AppHash)
	}
	if appID == 0 || appHash == "" {
		return fmt.Errorf("TELEGRAM_APP_ID and TELEGRAM_APP_HASH must be configured globally or on the account")
	}

	sessionFile := strings.TrimSpace(acc.SessionFile)
	if sessionFile == "" {
		sessionFile = filepath.Join(m.cfg.SessionDir, fmt.Sprintf("account_%d.json", acc.ID))
	}
	if dir := filepath.Dir(sessionFile); dir != "" && dir != "." {
		if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
			return fmt.Errorf("create session directory %s: %w", dir, mkErr)
		}
	}

	m.mu.Lock()
	if existing, ok := m.runtimes[accountID]; ok && existing.cancel != nil {
		// Already running or connecting
		st := existing.state.Status
		if st == domain.AccountStatusRunning || st == domain.AccountStatusConnecting ||
			st == domain.AccountStatusWaitingOTP || st == domain.AccountStatusWaiting2FA {
			m.mu.Unlock()
			return nil
		}
	}

	baseCtx := m.parentCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	runCtx, cancel := context.WithCancel(baseCtx)
	done := make(chan struct{})
	otpChan := make(chan string, 1)
	passChan := make(chan string, 1)

	rt := &accountRuntime{
		account:      *acc,
		cancel:       cancel,
		done:         done,
		otpChan:      otpChan,
		passwordChan: passChan,
		state:        domain.AccountRuntimeState{Status: domain.AccountStatusConnecting},
	}

	handler := NewAccountMessageHandler(
		acc.ID,
		AccountFeatureFlags{
			EnableAIGateway:    acc.EnableAIGateway,
			AISystemPrompt:     acc.AISystemPrompt,
			EnableClassAuction: acc.EnableClassAuction,
		},
		m.auctionUC,
		m.aiUC,
		func(ctx context.Context, peer tg.InputPeerClass, msgID int, text string) error {
			return m.Reply(ctx, acc.ID, peer, msgID, text)
		},
		m.logger,
	)

	tgClient, err := NewTelegramClient(appID, appHash, sessionFile, handler, m.logger)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return fmt.Errorf("init telegram client for account %d: %w", acc.ID, err)
	}

	rt.client = tgClient
	rt.handler = handler
	m.runtimes[accountID] = rt
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer m.wg.Done()
		defer close(done)

		cb := StartCallbacks{
			OTPProvider: func(ctx context.Context) (string, error) {
				m.setState(accountID, domain.AccountStatusWaitingOTP, nil, "")
				select {
				case code := <-otpChan:
					m.setState(accountID, domain.AccountStatusConnecting, nil, "")
					return code, nil
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(authWaitTimeout):
					return "", errors.New("OTP timeout after 5 minutes")
				}
			},
			PasswordProvider: func(ctx context.Context) (string, error) {
				m.setState(accountID, domain.AccountStatusWaiting2FA, nil, "")
				select {
				case pass := <-passChan:
					m.setState(accountID, domain.AccountStatusConnecting, nil, "")
					return pass, nil
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(authWaitTimeout):
					return "", errors.New("2FA password timeout after 5 minutes")
				}
			},
			OnStatusChange: func(status domain.AccountStatus, user *tg.User, errMsg string) {
				m.setState(accountID, status, user, errMsg)
			},
		}

		err := tgClient.Start(runCtx, acc.PhoneNumber, acc.TwoFAPassword, m.logger, cb)
		if err != nil && !errors.Is(err, context.Canceled) {
			m.setState(accountID, domain.AccountStatusError, nil, err.Error())
			if m.logger != nil {
				m.logger.Error("Telegram account stopped with error",
					zap.Uint("account_id", accountID),
					zap.String("name", acc.Name),
					zap.Error(err))
			}
		} else {
			m.setState(accountID, domain.AccountStatusStopped, nil, "")
		}

		m.mu.Lock()
		if cur, ok := m.runtimes[accountID]; ok && cur == rt {
			cur.cancel = nil
		}
		m.mu.Unlock()
	}()

	return nil
}

func (m *AccountManager) StopAccount(accountID uint) error {
	m.mu.Lock()
	rt, ok := m.runtimes[accountID]
	if !ok || rt.cancel == nil {
		if ok {
			rt.state.Status = domain.AccountStatusStopped
			rt.state.LastError = ""
		}
		m.mu.Unlock()
		return nil
	}
	cancel := rt.cancel
	done := rt.done
	rt.cancel = nil
	m.mu.Unlock()

	cancel()
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	return nil
}

func (m *AccountManager) RestartAccount(accountID uint) error {
	if err := m.StopAccount(accountID); err != nil {
		return err
	}
	return m.StartAccount(accountID)
}

func (m *AccountManager) SubmitOTP(accountID uint, code string) error {
	m.mu.RLock()
	rt, ok := m.runtimes[accountID]
	if !ok {
		m.mu.RUnlock()
		return domain.ErrAccountNotFound
	}
	status := rt.state.Status
	otpChan := rt.otpChan
	m.mu.RUnlock()

	if status != domain.AccountStatusWaitingOTP {
		return errors.New("account is not waiting for OTP")
	}

	select {
	case otpChan <- strings.TrimSpace(code):
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timeout: account is not accepting OTP right now")
	}
}

func (m *AccountManager) Submit2FA(accountID uint, password string) error {
	m.mu.RLock()
	rt, ok := m.runtimes[accountID]
	if !ok {
		m.mu.RUnlock()
		return domain.ErrAccountNotFound
	}
	status := rt.state.Status
	passChan := rt.passwordChan
	m.mu.RUnlock()

	if status != domain.AccountStatusWaiting2FA {
		return errors.New("account is not waiting for 2FA password")
	}

	select {
	case passChan <- password:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timeout: account is not accepting 2FA password right now")
	}
}

func (m *AccountManager) GetRuntimeState(accountID uint) domain.AccountRuntimeState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if rt, ok := m.runtimes[accountID]; ok {
		return rt.state
	}
	return domain.AccountRuntimeState{Status: domain.AccountStatusStopped}
}

func (m *AccountManager) RefreshAccountRuntime(account *domain.TelegramAccount) {
	if account == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// If this account became Default B2B, clear the cached default flag on sibling runtimes too
	if account.IsDefaultB2B {
		for id, other := range m.runtimes {
			if id != account.ID {
				other.account.IsDefaultB2B = false
			}
		}
	}

	rt, ok := m.runtimes[account.ID]
	if !ok {
		m.runtimes[account.ID] = &accountRuntime{
			account: *account,
			state:   domain.AccountRuntimeState{Status: domain.AccountStatusStopped},
		}
		return
	}
	rt.account = *account
	if rt.handler != nil {
		rt.handler.SetFlags(AccountFeatureFlags{
			EnableAIGateway:    account.EnableAIGateway,
			AISystemPrompt:     account.AISystemPrompt,
			EnableClassAuction: account.EnableClassAuction,
		})
	}
}

func (m *AccountManager) RemoveAccountRuntime(accountID uint) {
	_ = m.StopAccount(accountID)
	m.mu.Lock()
	delete(m.runtimes, accountID)
	m.mu.Unlock()
}

func (m *AccountManager) setState(accountID uint, status domain.AccountStatus, user *tg.User, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rt, ok := m.runtimes[accountID]
	if !ok {
		return
	}
	rt.state.Status = status
	rt.state.LastError = errMsg
	if user != nil {
		rt.state.TelegramUserID = user.ID
		rt.state.TelegramUsername = user.Username
	}
}

func (m *AccountManager) getRunningClient(accountID uint) (*TelegramClient, error) {
	m.mu.RLock()
	rt, ok := m.runtimes[accountID]
	if !ok {
		m.mu.RUnlock()
		return nil, domain.ErrAccountNotFound
	}
	status := rt.state.Status
	client := rt.client
	m.mu.RUnlock()

	if status != domain.AccountStatusRunning || client == nil {
		return nil, domain.ErrBotNotRunning
	}
	return client, nil
}

// ResolveB2BAccount implements usecase.MultiAccountMessageSender.
// When requestedAccountID == 0, selects the Default B2B account (or first B2B-enabled account).
// Explicitly refuses accounts where EnableB2B == false so Class Auction accounts stay isolated.
func (m *AccountManager) ResolveB2BAccount(requestedAccountID uint) (uint, domain.AccountStatus, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if requestedAccountID > 0 {
		rt, ok := m.runtimes[requestedAccountID]
		if !ok {
			return 0, domain.AccountStatusStopped, domain.ErrAccountNotFound
		}
		if !rt.account.EnableB2B {
			return 0, rt.state.Status, domain.ErrB2BNotEnabled
		}
		return rt.account.ID, rt.state.Status, nil
	}

	// Default B2B account lookup: prefer IsDefaultB2B && EnableB2B
	var fallback *accountRuntime
	for _, rt := range m.runtimes {
		if !rt.account.EnableB2B {
			continue
		}
		if rt.account.IsDefaultB2B {
			return rt.account.ID, rt.state.Status, nil
		}
		if fallback == nil || (fallback.state.Status != domain.AccountStatusRunning && rt.state.Status == domain.AccountStatusRunning) ||
			(rt.state.Status == fallback.state.Status && rt.account.ID < fallback.account.ID) {
			fallback = rt
		}
	}
	if fallback != nil {
		return fallback.account.ID, fallback.state.Status, nil
	}
	return 0, domain.AccountStatusStopped, domain.ErrB2BNotEnabled
}

func (m *AccountManager) SendMessage(ctx context.Context, accountID uint, chatID int64, topicID int, username string, text string) error {
	client, err := m.getRunningClient(accountID)
	if err != nil {
		return err
	}
	return client.SendMessage(ctx, chatID, topicID, username, text)
}

func (m *AccountManager) Reply(ctx context.Context, accountID uint, peer tg.InputPeerClass, msgID int, text string) error {
	client, err := m.getRunningClient(accountID)
	if err != nil {
		return err
	}
	return client.Reply(ctx, peer, msgID, text)
}

func (m *AccountManager) SendSavedMessage(ctx context.Context, accountID uint, text string) error {
	client, err := m.getRunningClient(accountID)
	if err != nil {
		return err
	}
	return client.SendSavedMessage(ctx, text)
}

func (m *AccountManager) SyncGroups(ctx context.Context, accountID uint) ([]domain.TelegramGroup, error) {
	client, err := m.getRunningClient(accountID)
	if err != nil {
		return nil, err
	}
	groups, err := client.GetGroups(ctx)
	if err != nil {
		return nil, err
	}

	for _, g := range groups {
		if err := m.groupRepo.Upsert(&domain.TelegramGroup{
			AccountID: accountID,
			ID:        g.ID,
			Title:     g.Title,
			Type:      g.Type,
		}); err != nil && m.logger != nil {
			m.logger.Error("Failed to upsert group", zap.Uint("account_id", accountID), zap.Int64("group_id", g.ID), zap.Error(err))
		}
	}
	return m.groupRepo.FindAllByAccount(accountID)
}

func (m *AccountManager) GetTopics(ctx context.Context, accountID uint, groupID int64) ([]domain.TopicInfo, error) {
	client, err := m.getRunningClient(accountID)
	if err != nil {
		return nil, err
	}
	return client.GetTopics(ctx, groupID)
}

func (m *AccountManager) Shutdown() {
	m.mu.Lock()
	for _, rt := range m.runtimes {
		if rt.cancel != nil {
			rt.cancel()
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
}
