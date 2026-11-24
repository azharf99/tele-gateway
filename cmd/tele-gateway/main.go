// cmd/tele-gateway/main.go
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/azharf99/tele-gateway/internal/config"
	deliveryHttp "github.com/azharf99/tele-gateway/internal/delivery/http"
	"github.com/azharf99/tele-gateway/internal/delivery/telegram"
	"github.com/azharf99/tele-gateway/internal/parser"
	"github.com/azharf99/tele-gateway/internal/repository"
	"github.com/azharf99/tele-gateway/internal/seeder"
	"github.com/azharf99/tele-gateway/internal/usecase"
	"go.uber.org/zap"
)

func main() {
	// 0. Init Logger
	logger, _ := config.InitLogger()
	defer logger.Sync()

	cfg := config.LoadConfig()

	// 1. Init Database
	db, err := repository.InitDB(cfg)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	// 2. Setup Repositories
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewTelegramAccountRepository(db, cfg.EncryptionKey)
	groupRepo := repository.NewTelegramGroupRepository(db)
	aiRepo := repository.NewAIContextRepository(db)
	auctionRepo := repository.NewClassAuctionRepository(db)

	// Seed default admin & auto-migrate existing single-account .env deployment as Account #1
	seeder.SeedAdmin(userRepo, logger)
	seeder.SeedLegacyAccount(accountRepo, cfg, logger)

	// 3. Setup UseCases & Telegram Multi-Account Manager
	aiUC, err := usecase.NewAIGatewayUseCase(aiRepo, cfg.GeminiAPIKey, logger)
	if err != nil {
		logger.Fatal("failed to init ai gateway usecase", zap.Error(err))
	}

	aiFallbackParser, err := parser.NewAIFallbackParser(cfg.GeminiAPIKey, logger)
	if err != nil {
		logger.Warn("AI fallback parser disabled due to init error", zap.Error(err))
	}

	auctionUC := usecase.NewClassAuctionUseCase(auctionRepo, nil, aiFallbackParser, logger)

	accountManager := telegram.NewAccountManager(cfg, accountRepo, groupRepo, auctionUC, aiUC, logger)
	if setter, ok := auctionUC.(interface {
		SetTelegramGateway(usecase.AuctionTelegramGateway)
	}); ok {
		setter.SetTelegramGateway(accountManager)
	}

	authUC := usecase.NewAuthUseCase(userRepo)
	userUC := usecase.NewUserUseCase(userRepo)
	messageUC := usecase.NewMessageUseCase(accountManager, logger)

	// 4. Init HTTP Delivery
	authHandler := deliveryHttp.NewAuthHandler(authUC)
	accountHandler := deliveryHttp.NewAccountHandler(cfg, accountRepo, groupRepo, auctionRepo, auctionUC, accountManager)
	auctionHandler := deliveryHttp.NewClassAuctionHandler(auctionUC)
	aiHandler := deliveryHttp.NewAIHandler(aiUC)
	userHandler := deliveryHttp.NewUserHandler(userUC)
	messageHandler := deliveryHttp.NewMessageHandler(messageUC)

	router := deliveryHttp.InitRouter(
		authHandler,
		accountHandler,
		auctionHandler,
		aiHandler,
		userHandler,
		messageHandler,
		cfg.B2BAPIKeys,
	)

	// 5. Run Services with Graceful Shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := accountManager.StartAllEnabled(ctx); err != nil {
		logger.Error("Failed to start enabled Telegram accounts", zap.Error(err))
	}

	if cfg.WatchdogTargetURL != "" && (cfg.WatchdogAlertChatID != 0 || cfg.WatchdogAlertUsername != "") {
		watchdog := usecase.NewHealthWatchdog(usecase.WatchdogConfig{
			TargetURL:      cfg.WatchdogTargetURL,
			HealthToken:    cfg.WatchdogHealthToken,
			Interval:       cfg.WatchdogInterval,
			FailThreshold:  cfg.WatchdogFailThreshold,
			ServiceName:    cfg.WatchdogServiceName,
			AlertAccountID: cfg.WatchdogAccountID,
			AlertChatID:    cfg.WatchdogAlertChatID,
			AlertTopicID:   cfg.WatchdogAlertTopicID,
			AlertUsername:  cfg.WatchdogAlertUsername,
		}, messageUC, logger)
		go watchdog.Start(ctx)
	}

	srv := &http.Server{
		Addr:    ":8080",
		Handler: router,
	}

	go func() {
		logger.Info("HTTP Server starting on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	logger.Info("Shutdown signal received, stopping HTTP server and Telegram accounts...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server forced shutdown", zap.Error(err))
	}
	accountManager.Shutdown()
	logger.Info("Tele-Gateway stopped cleanly")
}
