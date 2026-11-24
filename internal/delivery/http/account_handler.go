// internal/delivery/http/account_handler.go
package http

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azharf99/tele-gateway/internal/config"
	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

type AccountHandler struct {
	cfg         *config.Config
	accountRepo domain.TelegramAccountRepository
	groupRepo   domain.TelegramGroupRepository
	auctionRepo domain.ClassAuctionRepository
	auctionUC   domain.ClassAuctionUseCase
	manager     domain.AccountManager
}

func NewAccountHandler(
	cfg *config.Config,
	accountRepo domain.TelegramAccountRepository,
	groupRepo domain.TelegramGroupRepository,
	auctionRepo domain.ClassAuctionRepository,
	auctionUC domain.ClassAuctionUseCase,
	manager domain.AccountManager,
) *AccountHandler {
	return &AccountHandler{
		cfg:         cfg,
		accountRepo: accountRepo,
		groupRepo:   groupRepo,
		auctionRepo: auctionRepo,
		auctionUC:   auctionUC,
		manager:     manager,
	}
}

func (h *AccountHandler) populateRuntime(acc *domain.TelegramAccount) {
	if h.manager != nil {
		st := h.manager.GetRuntimeState(acc.ID)
		acc.Status = st.Status
		acc.LastError = st.LastError
		acc.TelegramUserID = st.TelegramUserID
		acc.TelegramUsername = st.TelegramUsername
	}
	acc.HasTwoFAPassword = acc.TwoFAPassword != ""
	acc.HasCustomAppHash = acc.AppHash != ""
}

func (h *AccountHandler) ListAccounts(c *gin.Context) {
	accounts, err := h.accountRepo.FindAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for i := range accounts {
		h.populateRuntime(&accounts[i])
	}
	c.JSON(http.StatusOK, accounts)
}

func (h *AccountHandler) CreateAccount(c *gin.Context) {
	var req domain.CreateAccountInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	phone := strings.TrimSpace(req.PhoneNumber)
	name := strings.TrimSpace(req.Name)
	if phone == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and phone_number are required"})
		return
	}

	isEnabled := true
	if req.IsEnabled != nil {
		isEnabled = *req.IsEnabled
	}

	enableB2B := req.EnableB2B || req.IsDefaultB2B

	acc := &domain.TelegramAccount{
		Name:               name,
		PhoneNumber:        phone,
		AppID:              req.AppID,
		AppHash:            strings.TrimSpace(req.AppHash),
		TwoFAPassword:      req.TwoFAPassword,
		SessionFile:        "pending",
		IsEnabled:          isEnabled,
		EnableB2B:          enableB2B,
		IsDefaultB2B:       req.IsDefaultB2B,
		EnableAIGateway:    req.EnableAIGateway,
		AISystemPrompt:     req.AISystemPrompt,
		EnableClassAuction: req.EnableClassAuction,
	}

	if err := h.accountRepo.Create(acc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Assign stable per-account session file inside SessionDir
	acc.SessionFile = filepath.Join(h.cfg.SessionDir, fmt.Sprintf("account_%d.json", acc.ID))
	if err := h.accountRepo.Update(acc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if acc.IsDefaultB2B {
		_ = h.accountRepo.ClearDefaultB2BExcept(acc.ID)
	}

	if h.manager != nil {
		h.manager.RefreshAccountRuntime(acc)
		if acc.IsEnabled {
			if err := h.manager.StartAccount(acc.ID); err != nil {
				acc.LastError = err.Error()
			}
		}
	}

	h.populateRuntime(acc)
	c.JSON(http.StatusCreated, acc)
}

func (h *AccountHandler) UpdateAccount(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	acc, err := h.accountRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var req domain.UpdateAccountInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	needsRestart := false
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		acc.Name = strings.TrimSpace(*req.Name)
	}
	if req.PhoneNumber != nil && strings.TrimSpace(*req.PhoneNumber) != "" && strings.TrimSpace(*req.PhoneNumber) != acc.PhoneNumber {
		acc.PhoneNumber = strings.TrimSpace(*req.PhoneNumber)
		needsRestart = true
	}
	if req.AppID != nil && *req.AppID != acc.AppID {
		acc.AppID = *req.AppID
		needsRestart = true
	}
	if req.ClearCustomAppHash {
		acc.AppHash = ""
		needsRestart = true
	} else if req.AppHash != nil && strings.TrimSpace(*req.AppHash) != "" {
		acc.AppHash = strings.TrimSpace(*req.AppHash)
		needsRestart = true
	}
	if req.ClearTwoFAPassword {
		acc.TwoFAPassword = ""
	} else if req.TwoFAPassword != nil && *req.TwoFAPassword != "" {
		acc.TwoFAPassword = *req.TwoFAPassword
	}
	if req.IsEnabled != nil {
		acc.IsEnabled = *req.IsEnabled
	}
	if req.EnableB2B != nil {
		acc.EnableB2B = *req.EnableB2B
		if !acc.EnableB2B {
			acc.IsDefaultB2B = false
		}
	}
	if req.IsDefaultB2B != nil {
		acc.IsDefaultB2B = *req.IsDefaultB2B
		if acc.IsDefaultB2B {
			acc.EnableB2B = true
		}
	}
	if req.EnableAIGateway != nil {
		acc.EnableAIGateway = *req.EnableAIGateway
	}
	if req.AISystemPrompt != nil {
		acc.AISystemPrompt = *req.AISystemPrompt
	}
	if req.EnableClassAuction != nil {
		acc.EnableClassAuction = *req.EnableClassAuction
	}

	if err := h.accountRepo.Update(acc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if acc.IsDefaultB2B {
		_ = h.accountRepo.ClearDefaultB2BExcept(acc.ID)
	}

	if h.manager != nil {
		h.manager.RefreshAccountRuntime(acc)
		if !acc.IsEnabled {
			_ = h.manager.StopAccount(acc.ID)
		} else if needsRestart {
			_ = h.manager.RestartAccount(acc.ID)
		}
	}

	h.populateRuntime(acc)
	c.JSON(http.StatusOK, acc)
}

func (h *AccountHandler) DeleteAccount(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	acc, err := h.accountRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if h.manager != nil {
		h.manager.RemoveAccountRuntime(id)
	}
	if h.auctionUC != nil {
		h.auctionUC.InvalidateAccountCache(id)
	}
	if h.auctionRepo != nil {
		_ = h.auctionRepo.DeleteByAccountID(id)
	}
	if h.groupRepo != nil {
		_ = h.groupRepo.DeleteByAccount(id)
	}
	if err := h.accountRepo.Delete(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Best-effort cleanup of per-account session file (only if it looks like account_<id>.json)
	if strings.Contains(filepath.Base(acc.SessionFile), fmt.Sprintf("account_%d", id)) {
		_ = os.Remove(acc.SessionFile)
	}

	c.JSON(http.StatusOK, gin.H{"message": "account deleted"})
}

func (h *AccountHandler) StartAccount(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	acc, err := h.accountRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !acc.IsEnabled {
		acc.IsEnabled = true
		_ = h.accountRepo.Update(acc)
	}

	if err := h.manager.StartAccount(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.populateRuntime(acc)
	c.JSON(http.StatusOK, acc)
}

func (h *AccountHandler) StopAccount(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}

	acc, err := h.accountRepo.FindByID(id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if acc.IsEnabled {
		acc.IsEnabled = false
		_ = h.accountRepo.Update(acc)
	}
	if err := h.manager.StopAccount(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.populateRuntime(acc)
	c.JSON(http.StatusOK, acc)
}

func (h *AccountHandler) SubmitOTP(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.manager.SubmitOTP(id, req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "OTP submitted"})
}

func (h *AccountHandler) Submit2FA(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		Password string `json:"password" binding:"required"`
		Save     bool   `json:"save"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Save {
		if acc, err := h.accountRepo.FindByID(id); err == nil {
			acc.TwoFAPassword = req.Password
			_ = h.accountRepo.Update(acc)
		}
	}
	if err := h.manager.Submit2FA(id, req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "2FA password submitted"})
}

func (h *AccountHandler) GetGroups(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	groups, err := h.groupRepo.FindAllByAccount(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, groups)
}

func (h *AccountHandler) SyncGroups(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	groups, err := h.manager.SyncGroups(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrBotNotRunning) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, domain.ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Groups synced successfully", "count": len(groups), "groups": groups})
}

func (h *AccountHandler) GetGroupTopics(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	groupID, err := strconv.ParseInt(c.Param("groupId"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	topics, err := h.manager.GetTopics(c.Request.Context(), id, groupID)
	if err != nil {
		if errors.Is(err, domain.ErrBotNotRunning) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, topics)
}

func parseUintParam(c *gin.Context, name string) (uint, error) {
	v, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || v == 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return uint(v), nil
}
