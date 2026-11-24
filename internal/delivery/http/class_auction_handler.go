// internal/delivery/http/class_auction_handler.go
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

type ClassAuctionHandler struct {
	uc domain.ClassAuctionUseCase
}

func NewClassAuctionHandler(uc domain.ClassAuctionUseCase) *ClassAuctionHandler {
	return &ClassAuctionHandler{uc: uc}
}

func (h *ClassAuctionHandler) GetCatalog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"courses":       domain.AllCourses(),
		"auction_types": []string{string(domain.AuctionTypeNew), string(domain.AuctionTypeOTR), string(domain.AuctionTypeCU), string(domain.AuctionTypeFR)},
		"formats":       []string{string(domain.ClassFormatPrivate), string(domain.ClassFormatGroup)},
		"languages":     []string{string(domain.LanguageIndonesia), string(domain.LanguageBilingual), string(domain.LanguageFullEnglish)},
		"genders":       []string{string(domain.GenderMale), string(domain.GenderFemale)},
	})
}

func (h *ClassAuctionHandler) GetProfile(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	profile, err := h.uc.GetProfile(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, profile)
}

func (h *ClassAuctionHandler) UpdateProfile(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req domain.ClassAuctionProfile
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	updated, err := h.uc.UpdateProfile(accountID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidAuctionConfig) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *ClassAuctionHandler) ListTargets(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	targets, err := h.uc.ListWatchedTargets(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, targets)
}

func (h *ClassAuctionHandler) AddTarget(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req domain.WatchedTarget
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	created, err := h.uc.AddWatchedTarget(accountID, &req)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidAuctionConfig):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrTargetAlreadyExists):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *ClassAuctionHandler) UpdateTarget(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	targetID, err := parseUintParam(c, "targetId")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target id"})
		return
	}
	var req domain.WatchedTarget
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ID = targetID
	updated, err := h.uc.UpdateWatchedTarget(accountID, &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *ClassAuctionHandler) DeleteTarget(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	targetID, err := parseUintParam(c, "targetId")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target id"})
		return
	}
	if err := h.uc.DeleteWatchedTarget(accountID, targetID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "target deleted"})
}

func (h *ClassAuctionHandler) GetWeeklySchedule(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	windows, err := h.uc.GetWeeklySchedule(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, windows)
}

func (h *ClassAuctionHandler) ReplaceWeeklySchedule(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req struct {
		Windows []domain.WeeklyAvailability `json:"windows"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	saved, err := h.uc.ReplaceWeeklySchedule(accountID, req.Windows)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidAuctionConfig) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (h *ClassAuctionHandler) ListDateOverrides(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	overrides, err := h.uc.ListDateOverrides(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, overrides)
}

func (h *ClassAuctionHandler) AddDateOverride(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req domain.DateAvailabilityOverride
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	created, err := h.uc.AddDateOverride(accountID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidAuctionConfig) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (h *ClassAuctionHandler) DeleteDateOverride(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	overrideID, err := parseUintParam(c, "overrideId")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid override id"})
		return
	}
	if err := h.uc.DeleteDateOverride(accountID, overrideID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "override deleted"})
}

func (h *ClassAuctionHandler) Simulate(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	var req domain.SimulateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	res, err := h.uc.Simulate(c.Request.Context(), accountID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (h *ClassAuctionHandler) ListLogs(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	page, err := h.uc.ListLogs(accountID, domain.AuctionLogFilter{
		Decision: c.Query("decision"),
		Outcome:  c.Query("outcome"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *ClassAuctionHandler) MarkLogOutcome(c *gin.Context) {
	accountID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
		return
	}
	logID, err := parseUintParam(c, "logId")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid log id"})
		return
	}
	var req struct {
		Outcome domain.AuctionOutcome `json:"outcome" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	entry, err := h.uc.MarkLogOutcome(c.Request.Context(), accountID, logID, req.Outcome)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidAuctionConfig):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrAuctionLogNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (h *ClassAuctionHandler) GetStats(c *gin.Context) {
	var accountID uint
	if raw := c.Param("id"); raw != "" {
		parsed, err := parseUintParam(c, "id")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account id"})
			return
		}
		accountID = parsed
	}
	stats, err := h.uc.GetStats(accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}
