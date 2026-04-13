// internal/repository/db.go
package repository

import (
	"fmt"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/config"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func InitDB(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Jakarta",
		cfg.DBHost, cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBPort)
	
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Auto Migration
	err = db.AutoMigrate(&domain.BidRule{}, &domain.User{}, &domain.TelegramGroup{})
	if err != nil {
		return nil, err
	}

	return db, nil
}
