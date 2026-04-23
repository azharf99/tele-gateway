// internal/delivery/http/router.go
package http

import (
	"os"
	"strings"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitRouter(authHandler *AuthHandler, bidHandler *BidHandler) *gin.Engine {
	r := gin.Default()

	r.SetTrustedProxies(nil)

	// KEAMANAN: Konfigurasi CORS Dinamis
	allowedOriginsEnv := os.Getenv("ALLOWED_ORIGINS")
	var allowedOrigins []string
	if allowedOriginsEnv == "" {
		allowedOrigins = []string{"http://localhost:5173"} // Fallback aman
	} else {
		allowedOrigins = strings.Split(allowedOriginsEnv, ",")
	}

	// CORS Setup
	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins, // For production, specify your frontend URL
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Public Routes
	r.POST("/login", authHandler.Login)
	r.POST("/refresh", authHandler.RefreshToken)

	// Protected Routes
	api := r.Group("/api")
	api.Use(AuthMiddleware())
	{
		// Profile Management
		api.PUT("/profile", authHandler.UpdateProfile)

		// Bid Rules Management
		api.GET("/rules", bidHandler.GetAllRules)
		api.POST("/rules", RoleMiddleware(domain.RoleAdmin), bidHandler.CreateRule)
		api.DELETE("/rules/:id", RoleMiddleware(domain.RoleAdmin), bidHandler.DeleteRule)

		// Bot Management
		api.GET("/bot/status", bidHandler.GetStatus)
		api.POST("/bot/otp", RoleMiddleware(domain.RoleAdmin), bidHandler.SubmitOTP)

		// Groups Management
		api.GET("/groups", bidHandler.GetGroups)
		api.GET("/groups/:id/topics", bidHandler.GetGroupTopics)
		api.POST("/groups/sync", RoleMiddleware(domain.RoleAdmin), bidHandler.SyncGroups)
	}

	return r
}
