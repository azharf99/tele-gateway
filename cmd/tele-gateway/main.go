// cmd/tele-gateway/main.go
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/azharf99/tele-gateway/internal/config"
	deliveryHttp "github.com/azharf99/tele-gateway/internal/delivery/http"
	"github.com/azharf99/tele-gateway/internal/delivery/telegram"
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

	// 2. Setup Dependency Injection
	userRepo := repository.NewUserRepository(db)
	bidRepo := repository.NewBidRepository(db)
	groupRepo := repository.NewTelegramGroupRepository(db)
	aiRepo := repository.NewAIContextRepository(db)

	// Create Default Admin if not exists
	seeder.SeedAdmin(userRepo, logger)

	handlerTg := &telegram.AuctionHandler{
		Logger: logger,
	}

	aiUC, err := usecase.NewAIGatewayUseCase(aiRepo, cfg.GeminiAPIKey, logger)
	if err != nil {
		logger.Fatal("failed to init ai gateway usecase", zap.Error(err))
	}
	handlerTg.AIUseCase = aiUC

	tgClient, err := telegram.NewTelegramClient(cfg.AppID, cfg.AppHash, cfg.SessionFile, handlerTg)
	if err != nil {
		logger.Fatal("failed to init telegram client", zap.Error(err))
	}

	auctionUC := usecase.NewAuctionUseCase(bidRepo, groupRepo, tgClient, logger)
	handlerTg.UseCase = auctionUC

	authUC := usecase.NewAuthUseCase(userRepo)
	userUC := usecase.NewUserUseCase(userRepo)

	// 3. Init HTTP Delivery
	authHandler := deliveryHttp.NewAuthHandler(authUC)
	bidHandler := deliveryHttp.NewBidHandler(auctionUC)
	aiHandler := deliveryHttp.NewAIHandler(aiUC)
	userHandler := deliveryHttp.NewUserHandler(userUC)
	router := deliveryHttp.InitRouter(authHandler, bidHandler, aiHandler, userHandler)

	// 4. Run Services
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run HTTP Server in background
	go func() {
		logger.Info("HTTP Server starting on :8080")
		if err := router.Run(":8080"); err != nil && err != http.ErrServerClosed {
			logger.Fatal("http server error", zap.Error(err))
		}
	}()

	// Run Telegram Client
	otpProvider := func(ctx context.Context) (string, error) {
		if auc, ok := auctionUC.(interface {
			WaitOTP(context.Context) (string, error)
		}); ok {
			return auc.WaitOTP(ctx)
		}
		return "", http.ErrNoCookie // Just a dummy error
	}

	onSuccess := func() {
		auctionUC.SetStatus("RUNNING")
	}

	if err := tgClient.Start(ctx, os.Getenv("PHONE_NUMBER"), os.Getenv("PASSWORD"), logger, otpProvider, onSuccess); err != nil {
		logger.Fatal("bot error", zap.Error(err))
	}
}
