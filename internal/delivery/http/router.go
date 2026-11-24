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

func InitRouter(
	authHandler *AuthHandler,
	accountHandler *AccountHandler,
	auctionHandler *ClassAuctionHandler,
	aiHandler *AIHandler,
	userHandler *UserHandler,
	messageHandler *MessageHandler,
	b2bAPIKeys []string,
) *gin.Engine {
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
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	api := r.Group("/api")

	// Public Routes
	api.POST("/login", authHandler.Login)
	api.POST("/refresh", authHandler.RefreshToken)

	// Backend-to-Backend Routes (X-API-Key atau JWT Admin)
	if messageHandler != nil {
		b2b := api.Group("/messages")
		b2b.Use(APIKeyOrAdminMiddleware(b2bAPIKeys))
		{
			b2b.POST("/send", messageHandler.SendMessage)
		}
	}

	// Protected Routes
	protected := api.Group("/")
	protected.Use(AuthMiddleware())
	{
		// Profile Management
		protected.PUT("/profile", authHandler.UpdateProfile)

		// Class Auction Catalog & Global Stats
		protected.GET("/auction/catalog", auctionHandler.GetCatalog)
		protected.GET("/auction/stats", auctionHandler.GetStats)

		// Multi-Account Telegram Management
		accounts := protected.Group("/accounts")
		{
			accounts.GET("", accountHandler.ListAccounts)
			accounts.POST("", RoleMiddleware(domain.RoleAdmin), accountHandler.CreateAccount)
			accounts.PUT("/:id", RoleMiddleware(domain.RoleAdmin), accountHandler.UpdateAccount)
			accounts.DELETE("/:id", RoleMiddleware(domain.RoleAdmin), accountHandler.DeleteAccount)
			accounts.POST("/:id/start", RoleMiddleware(domain.RoleAdmin), accountHandler.StartAccount)
			accounts.POST("/:id/stop", RoleMiddleware(domain.RoleAdmin), accountHandler.StopAccount)
			accounts.POST("/:id/otp", RoleMiddleware(domain.RoleAdmin), accountHandler.SubmitOTP)
			accounts.POST("/:id/2fa", RoleMiddleware(domain.RoleAdmin), accountHandler.Submit2FA)

			// Per-Account Groups & Forum Topics
			accounts.GET("/:id/groups", accountHandler.GetGroups)
			accounts.POST("/:id/groups/sync", RoleMiddleware(domain.RoleAdmin), accountHandler.SyncGroups)
			accounts.GET("/:id/groups/:groupId/topics", accountHandler.GetGroupTopics)

			// Per-Account Class Auction Management
			auction := accounts.Group("/:id/auction")
			{
				auction.GET("/profile", auctionHandler.GetProfile)
				auction.PUT("/profile", RoleMiddleware(domain.RoleAdmin), auctionHandler.UpdateProfile)

				auction.GET("/targets", auctionHandler.ListTargets)
				auction.POST("/targets", RoleMiddleware(domain.RoleAdmin), auctionHandler.AddTarget)
				auction.PUT("/targets/:targetId", RoleMiddleware(domain.RoleAdmin), auctionHandler.UpdateTarget)
				auction.DELETE("/targets/:targetId", RoleMiddleware(domain.RoleAdmin), auctionHandler.DeleteTarget)

				auction.GET("/schedule", auctionHandler.GetWeeklySchedule)
				auction.PUT("/schedule", RoleMiddleware(domain.RoleAdmin), auctionHandler.ReplaceWeeklySchedule)

				auction.GET("/overrides", auctionHandler.ListDateOverrides)
				auction.POST("/overrides", RoleMiddleware(domain.RoleAdmin), auctionHandler.AddDateOverride)
				auction.DELETE("/overrides/:overrideId", RoleMiddleware(domain.RoleAdmin), auctionHandler.DeleteDateOverride)

				auction.POST("/simulate", auctionHandler.Simulate)
				auction.GET("/logs", auctionHandler.ListLogs)
				auction.POST("/logs/:logId/outcome", RoleMiddleware(domain.RoleAdmin), auctionHandler.MarkLogOutcome)
				auction.GET("/stats", auctionHandler.GetStats)
			}
		}

		// Global AI Context Management (fallback default prompt)
		protected.POST("/ai/context", RoleMiddleware(domain.RoleAdmin), aiHandler.SetContext)
		protected.GET("/ai/context", aiHandler.GetContext)

		// User Management (Admin Only)
		users := protected.Group("/users")
		users.Use(RoleMiddleware(domain.RoleAdmin))
		{
			users.GET("", userHandler.GetAll)
			users.GET("/:id", userHandler.GetByID)
			users.POST("", userHandler.Create)
			users.PUT("/:id", userHandler.Update)
			users.DELETE("/:id", userHandler.Delete)
		}
	}

	return r
}
