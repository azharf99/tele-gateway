// internal/config/logger.go
package config

import (
	"go.uber.org/zap"
)

func InitLogger() (*zap.Logger, error) {
	return zap.NewProduction()
}
