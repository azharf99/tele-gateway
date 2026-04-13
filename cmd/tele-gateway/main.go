// cmd/tele-gateway/main.go
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/azharf99/tele-gateway/internal/config"
	deliveryHttp "github.com/azharf99/tele-gateway/internal/delivery/http"
	"github.com/azharf99/tele-gateway/internal/delivery/telegram"
	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/repository"
	"github.com/azharf99/tele-gateway/internal/usecase"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
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

	// Create Default Admin if not exists
	seedAdmin(userRepo, logger)

	handlerTg := &telegram.AuctionHandler{
		Logger: logger,
	}

	tgClient, err := telegram.NewTelegramClient(cfg.AppID, cfg.AppHash, cfg.SessionFile, handlerTg)
	if err != nil {
		logger.Fatal("failed to init telegram client", zap.Error(err))
	}

	auctionUC := usecase.NewAuctionUseCase(bidRepo, groupRepo, tgClient, logger)
	handlerTg.UseCase = auctionUC

	authUC := usecase.NewAuthUseCase(userRepo)

	// 3. Init HTTP Delivery
	authHandler := deliveryHttp.NewAuthHandler(authUC)
	bidHandler := deliveryHttp.NewBidHandler(auctionUC)
	router := deliveryHttp.InitRouter(authHandler, bidHandler)

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
	if err := tgClient.Client.Run(ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(
			auth.Constant(os.Getenv("PHONE_NUMBER"), os.Getenv("PASSWORD"), auth.CodeAuthenticatorFunc(func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
				logger.Info("Waiting for OTP from Frontend/Console...")
				
				// Try to get OTP from frontend via usecase channel
				if auc, ok := auctionUC.(interface {
					WaitOTP(context.Context) (string, error)
				}); ok {
					code, err := auc.WaitOTP(ctx)
					if err == nil {
						return code, nil
					}
					logger.Warn("Failed to get OTP from Frontend, falling back to Console", zap.Error(err))
				}

				fmt.Print("Enter code (Console Fallback): ")
				code, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				return strings.TrimSpace(code), nil
			})),
			auth.SendCodeOptions{},
		)

		if err := tgClient.Client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}

		logger.Info("Userbot is running and monitoring...")
		<-ctx.Done()
		return nil
	}); err != nil {
		logger.Fatal("bot error", zap.Error(err))
	}
}

func seedAdmin(repo domain.UserRepository, logger *zap.Logger) {
	email := "admin@tele-gateway.com"
	if _, err := repo.FindByEmail(email); err != nil {
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
		user := &domain.User{
			Name:     "Super Admin",
			Email:    email,
			Password: string(hashedPassword),
			Role:     domain.RoleAdmin,
		}
		if err := repo.Create(user); err != nil {
			logger.Error("failed to seed admin", zap.Error(err))
		} else {
			logger.Info("Default admin created: admin@tele-gateway.com / admin123")
		}
	}
}
