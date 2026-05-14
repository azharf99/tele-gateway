// internal/seeder/seeder.go
package seeder

import (
	"os"

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
			logger.Info("Default admin created", zap.String("email", email), zap.String("password", password))
		}
	}
}
