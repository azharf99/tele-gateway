// internal/delivery/http/handler.go
package http

import (
	"net/http"
	"strconv"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	authUseCase domain.AuthUseCase
}

func NewAuthHandler(authUseCase domain.AuthUseCase) *AuthHandler {
	return &AuthHandler{authUseCase: authUseCase}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	accessToken, refreshToken, err := h.authUseCase.Login(req.Email, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

type BidHandler struct {
	auctionUseCase domain.AuctionUseCase
}

func NewBidHandler(auctionUseCase domain.AuctionUseCase) *BidHandler {
	return &BidHandler{auctionUseCase: auctionUseCase}
}

func (h *BidHandler) CreateRule(c *gin.Context) {
	var rule domain.BidRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.auctionUseCase.CreateRule(&rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, rule)
}

func (h *BidHandler) GetAllRules(c *gin.Context) {
	rules, err := h.auctionUseCase.GetAllRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rules)
}

func (h *BidHandler) DeleteRule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.auctionUseCase.DeleteRule(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Rule deleted"})
}

func (h *BidHandler) SubmitOTP(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.auctionUseCase.SubmitOTP(req.Code); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "OTP submitted"})
}

func (h *BidHandler) GetStatus(c *gin.Context) {
	status := h.auctionUseCase.GetStatus()
	c.JSON(http.StatusOK, gin.H{"status": status})
}

func (h *BidHandler) SyncGroups(c *gin.Context) {
	if err := h.auctionUseCase.SyncGroups(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Groups synced successfully"})
}

func (h *BidHandler) GetGroups(c *gin.Context) {
	groups, err := h.auctionUseCase.GetAllGroups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, groups)
}
