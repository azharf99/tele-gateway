// internal/delivery/http/bid_handler.go
package http

import (
	"errors"
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

// ImportRules bulk-seeds bid rules from an uploaded CSV file (multipart field
// "file") using upsert-by-keyword: existing keywords are replaced, new ones are
// created. Valid rows are committed atomically; invalid rows are skipped and
// reported so a single bad line never blocks the whole import.
func (h *BidHandler) ImportRules(c *gin.Context) {
	// Cap the request body before touching multipart parsing to bound memory use.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportBytes)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV file is required (multipart field 'file')"})
		return
	}

	if fileHeader.Size > maxImportBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large (max 2 MiB)"})
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to open uploaded file"})
		return
	}
	defer f.Close()

	rules, rowErrors, err := parseBidRuleCSV(f)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var created, updated int
	if len(rules) > 0 {
		created, updated, err = h.auctionUseCase.ImportRules(rules)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to import rules: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"created":     created,
		"updated":     updated,
		"skipped":     len(rowErrors),
		"total_valid": len(rules),
		"errors":      rowErrors,
	})
}

func (h *BidHandler) DeleteRule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.auctionUseCase.DeleteRule(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Rule deleted"})
}

// BulkDeleteRules removes many rules in a single request. A CSV import can seed
// hundreds of rules at once, and clearing them one DELETE at a time from the UI
// is both slow and easy to get wrong halfway through.
//
// The response reports `deleted` separately from `requested`: IDs that were
// already gone are not an error, they simply don't count towards `deleted`.
func (h *BidHandler) BulkDeleteRules(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "field 'ids' must be a non-empty array of rule ids"})
		return
	}

	deleted, err := h.auctionUseCase.DeleteRules(req.IDs)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidRuleSelection) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"deleted":   deleted,
		"requested": len(req.IDs),
	})
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
