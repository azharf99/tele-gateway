// internal/delivery/http/bid_handler.go
package http

import (
	"net/http"
	"strconv"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

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

func (h *BidHandler) UpdateRule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var rule domain.BidRule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rule.ID = uint(id)

	if err := h.auctionUseCase.UpdateRule(&rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, rule)
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

func (h *BidHandler) GetGroupTopics(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}

	topics, err := h.auctionUseCase.GetTopicsByGroup(c.Request.Context(), groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, topics)
}
