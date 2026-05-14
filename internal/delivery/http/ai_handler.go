// internal/delivery/http/ai_handler.go
package http

import (
	"net/http"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

type AIHandler struct {
	aiUseCase domain.AIGatewayUseCase
}

func NewAIHandler(aiUseCase domain.AIGatewayUseCase) *AIHandler {
	return &AIHandler{aiUseCase: aiUseCase}
}

func (h *AIHandler) SetContext(c *gin.Context) {
	var req struct {
		Key   string `json:"key" binding:"required"`
		Value string `json:"value" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.aiUseCase.SetAIContext(req.Key, req.Value); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "AI context updated"})
}

func (h *AIHandler) GetContext(c *gin.Context) {
	key := c.Query("key")
	if key == "" {
		key = "system_prompt"
	}

	value, err := h.aiUseCase.GetAIContext(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Context not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "value": value})
}
