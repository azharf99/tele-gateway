// internal/delivery/http/router.go
package http

import (
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitRouter(authHandler *AuthHandler, bidHandler *BidHandler) *gin.Engine {
	r := gin.Default()

	// CORS Setup
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"}, // For production, specify your frontend URL
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Public Routes
	r.POST("/login", authHandler.Login)

	// Protected Routes
	api := r.Group("/api")
	api.Use(AuthMiddleware())
	{
		// Bid Rules Management
		api.GET("/rules", bidHandler.GetAllRules)
		api.POST("/rules", RoleMiddleware(domain.RoleAdmin), bidHandler.CreateRule)
		api.DELETE("/rules/:id", RoleMiddleware(domain.RoleAdmin), bidHandler.DeleteRule)

		// Bot Management
		api.GET("/bot/status", bidHandler.GetStatus)
		api.POST("/bot/otp", RoleMiddleware(domain.RoleAdmin), bidHandler.SubmitOTP)

		// Groups Management
		api.GET("/groups", bidHandler.GetGroups)
		api.POST("/groups/sync", RoleMiddleware(domain.RoleAdmin), bidHandler.SyncGroups)
	}

	return r
}
