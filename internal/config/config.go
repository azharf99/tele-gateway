// internal/config/config.go
package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	AppID       int
	AppHash     string
	SessionFile string
	DBHost      string
	DBUser      string
	DBPass      string
	DBName      string
	DBPort      string
}

func LoadConfig() *Config {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: No .env file found, using system environment variables")
	}

	appID, _ := strconv.Atoi(os.Getenv("TELEGRAM_APP_ID"))
	return &Config{
		AppID:       appID,
		AppHash:     os.Getenv("TELEGRAM_APP_HASH"),
		SessionFile: os.Getenv("TELEGRAM_SESSION_FILE"),
		DBHost:      os.Getenv("DB_HOST"),
		DBUser:      os.Getenv("DB_USER"),
		DBPass:      os.Getenv("DB_PASS"),
		DBName:      os.Getenv("DB_NAME"),
		DBPort:      os.Getenv("DB_PORT"),
	}
}
