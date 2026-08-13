// internal/repository/db.go
package repository

import (
	"fmt"
	"time"

	"github.com/azharf99/tele-gateway/internal/config"
	"github.com/azharf99/tele-gateway/internal/domain"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	dbConnectAttempts = 15
	dbConnectBackoff  = 2 * time.Second
)

func InitDB(cfg *config.Config) (*gorm.DB, error) {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Jakarta",
		cfg.DBHost, cfg.DBUser, cfg.DBPass, cfg.DBName, cfg.DBPort)

	// Postgres lives in another compose stack, so a co-ordinated restart routinely
	// puts us here before it is ready ("the database system is starting up") or
	// even before its hostname resolves. Both are transient and used to kill the
	// process outright, taking the userbot down with them — retry instead.
	var (
		db  *gorm.DB
		err error
	)
	for attempt := 1; attempt <= dbConnectAttempts; attempt++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		if err == nil {
			var sqlDB interface{ Ping() error }
			if sqlDB, err = db.DB(); err == nil {
				err = sqlDB.Ping()
			}
		}
		if err == nil {
			break
		}
		if attempt == dbConnectAttempts {
			return nil, fmt.Errorf("database still unreachable after %d attempts: %w", dbConnectAttempts, err)
		}
		time.Sleep(dbConnectBackoff)
	}

	// Auto Migration
	err = db.AutoMigrate(&domain.BidRule{}, &domain.User{}, &domain.TelegramGroup{}, &domain.AIContext{})
	if err != nil {
		return nil, err
	}

	return db, nil
}
