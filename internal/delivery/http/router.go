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

	// Buat grup utama "/api" untuk semua request dari frontend
	api := r.Group("/api")

	// Public Routes (Tanpa Middleware Auth)
	api.POST("/login", authHandler.Login)
	api.POST("/refresh", authHandler.RefreshToken)

	// Protected Routes
	protected := api.Group("/")
	protected.Use(AuthMiddleware())
	{
		// Profile Management
		protected.PUT("/profile", authHandler.UpdateProfile)

		// Bid Rules Management
		protected.GET("/rules", bidHandler.GetAllRules)
		protected.POST("/rules", RoleMiddleware(domain.RoleAdmin), bidHandler.CreateRule)
		protected.PUT("/rules/:id", RoleMiddleware(domain.RoleAdmin), bidHandler.UpdateRule)
		protected.DELETE("/rules/:id", RoleMiddleware(domain.RoleAdmin), bidHandler.DeleteRule)

		// Bot Management
		protected.GET("/bot/status", bidHandler.GetStatus)
		protected.POST("/bot/otp", RoleMiddleware(domain.RoleAdmin), bidHandler.SubmitOTP)

		// Groups Management
		protected.GET("/groups", bidHandler.GetGroups)
		protected.GET("/groups/:id/topics", bidHandler.GetGroupTopics)
		protected.POST("/groups/sync", RoleMiddleware(domain.RoleAdmin), bidHandler.SyncGroups)
	}

	return r
}
