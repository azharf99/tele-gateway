// internal/seeder/seeder.go
package seeder

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/azharf99/tele-gateway/internal/config"
	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func SeedAdmin(repo domain.UserRepository, logger *zap.Logger) {
	email := os.Getenv("ADMIN_EMAIL")
	if email == "" {
		logger.Warn("ADMIN_EMAIL not set, skipping admin seeding")
		return
	}

	if _, err := repo.FindByEmail(email); err != nil {
		password := os.Getenv("ADMIN_PASSWORD")
		if password == "" {
			password = "admin123" // fallback
		}

		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		user := &domain.User{
			Name:     os.Getenv("ADMIN_NAME"),
			Email:    email,
			Password: string(hashedPassword),
			Role:     domain.RoleAdmin,
		}

		if user.Name == "" {
			user.Name = "Administrator"
		}

		if err := repo.Create(user); err != nil {
			logger.Error("failed to seed admin", zap.Error(err))
		} else {
			logger.Info("Default admin created", zap.String("email", email))
		}
	}
}

// SeedLegacyAccount migrates an existing single-account .env deployment into Account #1
// automatically so existing B2B, Watchdog, and AI DM sessions keep running without re-OTP.
func SeedLegacyAccount(repo domain.TelegramAccountRepository, cfg *config.Config, logger *zap.Logger) {
	count, err := repo.Count()
	if err != nil {
		logger.Warn("Failed to count telegram accounts for legacy migration", zap.Error(err))
		return
	}
	if count > 0 {
		return
	}
	phone := strings.TrimSpace(cfg.PhoneNumber)
	if phone == "" {
		logger.Info("No telegram accounts in DB and PHONE_NUMBER not set; add accounts from Web UI")
		return
	}

	sessionFile := strings.TrimSpace(cfg.SessionFile)
	if sessionFile == "" {
		sessionFile = filepath.Join(cfg.SessionDir, "session.json")
	}

	acc := &domain.TelegramAccount{
		Name:               "Primary B2B Account",
		PhoneNumber:        phone,
		TwoFAPassword:      cfg.Password,
		SessionFile:        sessionFile,
		IsEnabled:          true,
		EnableB2B:          true,
		IsDefaultB2B:       true,
		EnableAIGateway:    true,
		EnableClassAuction: false,
	}

	if err := repo.Create(acc); err != nil {
		logger.Error("Failed to migrate legacy .env Telegram account", zap.Error(err))
		return
	}
	logger.Info("Migrated legacy .env Telegram account as Primary B2B Account",
		zap.Uint("account_id", acc.ID),
		zap.String("phone", phone),
		zap.String("session_file", sessionFile),
	)
}
