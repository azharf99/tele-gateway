// internal/config/config.go
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultWatchdogInterval      = 60 * time.Second
	minWatchdogInterval          = 15 * time.Second
	defaultWatchdogFailThreshold = 2
)

type Config struct {
	AppID                 int
	AppHash               string
	SessionFile           string
	DBHost                string
	DBUser                string
	DBPass                string
	DBName                string
	DBPort                string
	GeminiAPIKey          string
	B2BAPIKeys            []string
	WatchdogTargetURL     string
	WatchdogHealthToken   string
	WatchdogInterval      time.Duration
	WatchdogFailThreshold int
	WatchdogServiceName   string
	WatchdogAlertChatID   int64
	WatchdogAlertTopicID  int
	WatchdogAlertUsername string
}

func LoadConfig() *Config {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		log.Println("Warning: No .env file found, using system environment variables")
	}

	appID, _ := strconv.Atoi(os.Getenv("TELEGRAM_APP_ID"))

	interval := defaultWatchdogInterval
	if rawInterval := strings.TrimSpace(os.Getenv("WATCHDOG_INTERVAL_SECONDS")); rawInterval != "" {
		if sec, convErr := strconv.Atoi(rawInterval); convErr == nil && sec > 0 {
			interval = time.Duration(sec) * time.Second
			if interval < minWatchdogInterval {
				interval = minWatchdogInterval
			}
		}
	}

	failThreshold := defaultWatchdogFailThreshold
	if rawThresh := strings.TrimSpace(os.Getenv("WATCHDOG_FAIL_THRESHOLD")); rawThresh != "" {
		if th, convErr := strconv.Atoi(rawThresh); convErr == nil {
			failThreshold = th
			if failThreshold < 1 {
				failThreshold = 1
			}
		}
	}

	serviceName := strings.TrimSpace(os.Getenv("WATCHDOG_SERVICE_NAME"))
	if serviceName == "" {
		serviceName = "wa-gateway"
	}

	var alertChatID int64
	if rawChatID := strings.TrimSpace(os.Getenv("WATCHDOG_ALERT_CHAT_ID")); rawChatID != "" {
		alertChatID, _ = strconv.ParseInt(rawChatID, 10, 64)
	}

	var alertTopicID int
	if rawTopicID := strings.TrimSpace(os.Getenv("WATCHDOG_ALERT_TOPIC_ID")); rawTopicID != "" {
		alertTopicID, _ = strconv.Atoi(rawTopicID)
	}

	return &Config{
		AppID:                 appID,
		AppHash:               os.Getenv("TELEGRAM_APP_HASH"),
		SessionFile:           os.Getenv("TELEGRAM_SESSION_FILE"),
		DBHost:                os.Getenv("DB_HOST"),
		DBUser:                os.Getenv("DB_USER"),
		DBPass:                os.Getenv("DB_PASS"),
		DBName:                os.Getenv("DB_NAME"),
		DBPort:                os.Getenv("DB_PORT"),
		GeminiAPIKey:          os.Getenv("GEMINI_API_KEY"),
		B2BAPIKeys:            parseCSVList(os.Getenv("B2B_API_KEYS")),
		WatchdogTargetURL:     strings.TrimSpace(os.Getenv("WATCHDOG_TARGET_URL")),
		WatchdogHealthToken:   strings.TrimSpace(os.Getenv("WATCHDOG_HEALTH_TOKEN")),
		WatchdogInterval:      interval,
		WatchdogFailThreshold: failThreshold,
		WatchdogServiceName:   serviceName,
		WatchdogAlertChatID:   alertChatID,
		WatchdogAlertTopicID:  alertTopicID,
		WatchdogAlertUsername: strings.TrimSpace(os.Getenv("WATCHDOG_ALERT_USERNAME")),
	}
}

func parseCSVList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
