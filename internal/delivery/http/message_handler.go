// internal/delivery/http/message_handler.go
package http

import (
	"errors"
	"net/http"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

const maxSendMessageBodyBytes = 64 << 10 // 64 KiB

type MessageHandler struct {
	messageUseCase domain.MessageUseCase
}

func NewMessageHandler(messageUseCase domain.MessageUseCase) *MessageHandler {
	return &MessageHandler{messageUseCase: messageUseCase}
}

func (h *MessageHandler) SendMessage(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxSendMessageBodyBytes)

	var req domain.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.messageUseCase.SendMessage(c.Request.Context(), req); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidTarget):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrTargetNotFound), errors.Is(err, domain.ErrAccountNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrB2BNotEnabled):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		case errors.Is(err, domain.ErrBotNotRunning):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "sent"})
}
