// internal/usecase/watchdog.go
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
)

const (
	defaultWatchdogReminderInterval = 30 * time.Minute
	defaultWatchdogHTTPTimeout      = 10 * time.Second
)

type WatchdogConfig struct {
	TargetURL        string
	HealthToken      string
	Interval         time.Duration
	FailThreshold    int
	ServiceName      string
	AlertAccountID   uint
	AlertChatID      int64
	AlertTopicID     int
	AlertUsername    string
	ReminderInterval time.Duration
}

type healthResponse struct {
	Status                string `json:"status"`
	DBOk                  *bool  `json:"db_ok"`
	WAVersion             string `json:"wa_version"`
	ExpectedConnected     int    `json:"expected_connected"`
	ActualConnected       int    `json:"actual_connected"`
	DisconnectedDeviceIDs []uint `json:"disconnected_device_ids"`
}

type HealthWatchdog struct {
	cfg        WatchdogConfig
	msgUC      domain.MessageUseCase
	httpClient *http.Client
	logger     *zap.Logger
	now        func() time.Time

	mu                  sync.Mutex
	consecutiveFailures int
	alerting            bool
	downSince           time.Time
	lastAlertAt         time.Time
}

func NewHealthWatchdog(cfg WatchdogConfig, msgUC domain.MessageUseCase, logger *zap.Logger) *HealthWatchdog {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.FailThreshold < 1 {
		cfg.FailThreshold = 2
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "wa-gateway"
	}
	if cfg.ReminderInterval <= 0 {
		cfg.ReminderInterval = defaultWatchdogReminderInterval
	}
	return &HealthWatchdog{
		cfg:        cfg,
		msgUC:      msgUC,
		httpClient: &http.Client{Timeout: defaultWatchdogHTTPTimeout},
		logger:     logger,
		now:        time.Now,
	}
}

func (w *HealthWatchdog) Start(ctx context.Context) {
	if w == nil || w.cfg.TargetURL == "" || (w.cfg.AlertChatID == 0 && w.cfg.AlertUsername == "") {
		return
	}

	if w.logger != nil {
		w.logger.Info("Health watchdog started",
			zap.String("target", w.cfg.TargetURL),
			zap.Duration("interval", w.cfg.Interval),
			zap.Int("fail_threshold", w.cfg.FailThreshold),
		)
	}

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.CheckOnce(ctx)
		}
	}
}

func (w *HealthWatchdog) CheckOnce(ctx context.Context) {
	reason, info, healthy := w.probe(ctx)
	now := w.now()

	w.mu.Lock()
	if healthy {
		w.consecutiveFailures = 0
		wasAlerting := w.alerting
		downDuration := now.Sub(w.downSince).Round(time.Second)
		w.alerting = false
		w.downSince = time.Time{}
		w.lastAlertAt = time.Time{}
		w.mu.Unlock()

		if wasAlerting {
			msg := fmt.Sprintf("✅ [RESOLVED] %s kembali sehat\nDurasi gangguan: %s\nDevice aktif: %d/%d\nVersi WA Web: %s",
				w.cfg.ServiceName, downDuration, info.ActualConnected, info.ExpectedConnected, nonEmptyOr(info.WAVersion, "-"))
			if err := w.sendAlert(ctx, msg); err != nil && w.logger != nil {
				w.logger.Warn("Watchdog failed to send RESOLVED alert", zap.Error(err))
			}
		}
		return
	}

	w.consecutiveFailures++
	if w.consecutiveFailures < w.cfg.FailThreshold {
		w.mu.Unlock()
		if w.logger != nil {
			w.logger.Warn("Watchdog probe unhealthy (below threshold)",
				zap.String("service", w.cfg.ServiceName),
				zap.Int("failures", w.consecutiveFailures),
				zap.Int("threshold", w.cfg.FailThreshold),
				zap.String("reason", reason),
			)
		}
		return
	}

	shouldSend := false
	isReminder := false
	if !w.alerting {
		shouldSend = true
		if w.downSince.IsZero() {
			w.downSince = now
		}
	} else if now.Sub(w.lastAlertAt) >= w.cfg.ReminderInterval {
		shouldSend = true
		isReminder = true
	}
	failures := w.consecutiveFailures
	downSince := w.downSince
	w.mu.Unlock()

	if !shouldSend {
		return
	}

	header := "🚨 [CRITICAL]"
	if isReminder {
		header = "🚨 [REMINDER CRITICAL]"
	}
	msg := fmt.Sprintf("%s %s DOWN / UNHEALTHY\nAlasan: %s\nGagal berturut-turut: %d kali\nDevice aktif: %d/%d\nVersi WA Web: %s\nSejak: %s",
		header,
		w.cfg.ServiceName,
		reason,
		failures,
		info.ActualConnected,
		info.ExpectedConnected,
		nonEmptyOr(info.WAVersion, "-"),
		downSince.Format("2006-01-02 15:04:05 MST"),
	)
	if len(info.DisconnectedDeviceIDs) > 0 {
		msg += fmt.Sprintf("\nDevice terputus IDs: %v", info.DisconnectedDeviceIDs)
	}

	if err := w.sendAlert(ctx, msg); err != nil {
		if w.logger != nil {
			w.logger.Warn("Watchdog failed to send CRITICAL alert", zap.Error(err))
		}
		return
	}

	w.mu.Lock()
	w.alerting = true
	w.lastAlertAt = now
	w.mu.Unlock()
}

func (w *HealthWatchdog) probe(ctx context.Context) (string, healthResponse, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, defaultWatchdogHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, w.cfg.TargetURL, nil)
	if err != nil {
		return fmt.Sprintf("invalid healthz url: %v", err), healthResponse{}, false
	}
	if w.cfg.HealthToken != "" {
		req.Header.Set("X-Health-Token", w.cfg.HealthToken)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("request error: %v", err), healthResponse{}, false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	var parsed healthResponse
	_ = json.Unmarshal(body, &parsed)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Sprintf("HTTP %d (%s)", resp.StatusCode, nonEmptyOr(parsed.Status, strings.TrimSpace(string(body)))), parsed, false
	}
	if parsed.DBOk != nil && !*parsed.DBOk {
		return "database ping failed (db_ok=false)", parsed, false
	}
	if parsed.ExpectedConnected > 0 && parsed.ActualConnected == 0 {
		return fmt.Sprintf("all expected devices disconnected (0/%d)", parsed.ExpectedConnected), parsed, false
	}
	return "", parsed, true
}

func (w *HealthWatchdog) sendAlert(ctx context.Context, message string) error {
	return w.msgUC.SendMessage(ctx, domain.SendMessageRequest{
		AccountID: w.cfg.AlertAccountID,
		ChatID:    w.cfg.AlertChatID,
		TopicID:   w.cfg.AlertTopicID,
		Username:  w.cfg.AlertUsername,
		Message:   message,
	})
}

func nonEmptyOr(val, fallback string) string {
	if strings.TrimSpace(val) == "" {
		return fallback
	}
	return val
}
