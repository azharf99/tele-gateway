// internal/usecase/watchdog_test.go
package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
)

type recordingMsgUC struct {
	mu       sync.Mutex
	requests []domain.SendMessageRequest
	err      error
}

func (r *recordingMsgUC) SendMessage(_ context.Context, req domain.SendMessageRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.requests = append(r.requests, req)
	return nil
}

func (r *recordingMsgUC) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

func (r *recordingMsgUC) last() domain.SendMessageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests[len(r.requests)-1]
}

func TestHealthWatchdog_ThresholdAndRecovery(t *testing.T) {
	var (
		mu         sync.Mutex
		statusCode = http.StatusOK
		payload    = map[string]any{
			"status":             "ok",
			"db_ok":              true,
			"wa_version":         "2.3000.1056020247",
			"expected_connected": 3,
			"actual_connected":   3,
		}
		gotToken string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotToken = r.Header.Get("X-Health-Token")
		code := statusCode
		body := payload
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	msgUC := &recordingMsgUC{}
	now := time.Unix(1700000000, 0)
	wd := NewHealthWatchdog(WatchdogConfig{
		TargetURL:        srv.URL,
		HealthToken:      "secret-health-token",
		Interval:         time.Minute,
		FailThreshold:    2,
		ServiceName:      "wa-gateway",
		AlertChatID:      123456,
		AlertTopicID:     7,
		ReminderInterval: 30 * time.Minute,
	}, msgUC, zap.NewNop())
	wd.now = func() time.Time { return now }

	// 1. Healthy check sends nothing and forwards token.
	wd.CheckOnce(context.Background())
	if msgUC.count() != 0 {
		t.Fatalf("expected 0 alerts on healthy check, got %d", msgUC.count())
	}
	mu.Lock()
	if gotToken != "secret-health-token" {
		t.Fatalf("X-Health-Token = %q, want secret-health-token", gotToken)
	}
	mu.Unlock()

	// 2. First failure (below threshold 2) does not alert yet.
	mu.Lock()
	statusCode = http.StatusServiceUnavailable
	payload = map[string]any{
		"status":             "unhealthy",
		"db_ok":              true,
		"wa_version":         "2.3000.1056020247",
		"expected_connected": 3,
		"actual_connected":   0,
	}
	mu.Unlock()

	wd.CheckOnce(context.Background())
	if msgUC.count() != 0 {
		t.Fatalf("expected 0 alerts below threshold, got %d", msgUC.count())
	}

	// 3. Second consecutive failure triggers CRITICAL alert.
	now = now.Add(time.Minute)
	wd.CheckOnce(context.Background())
	if msgUC.count() != 1 {
		t.Fatalf("expected 1 CRITICAL alert, got %d", msgUC.count())
	}
	if !strings.Contains(msgUC.last().Message, "[CRITICAL]") || msgUC.last().TopicID != 7 {
		t.Fatalf("unexpected alert: %+v", msgUC.last())
	}

	// 4. Third failure within reminder window does not spam.
	now = now.Add(time.Minute)
	wd.CheckOnce(context.Background())
	if msgUC.count() != 1 {
		t.Fatalf("expected no duplicate alert within reminder interval, got %d", msgUC.count())
	}

	// 5. Failure after reminder interval sends reminder.
	now = now.Add(31 * time.Minute)
	wd.CheckOnce(context.Background())
	if msgUC.count() != 2 || !strings.Contains(msgUC.last().Message, "[REMINDER CRITICAL]") {
		t.Fatalf("expected reminder alert, got count=%d msg=%q", msgUC.count(), msgUC.last().Message)
	}

	// 6. Recovery sends RESOLVED.
	mu.Lock()
	statusCode = http.StatusOK
	payload = map[string]any{
		"status":             "ok",
		"db_ok":              true,
		"wa_version":         "2.3000.1056020247",
		"expected_connected": 3,
		"actual_connected":   3,
	}
	mu.Unlock()

	now = now.Add(time.Minute)
	wd.CheckOnce(context.Background())
	if msgUC.count() != 3 || !strings.Contains(msgUC.last().Message, "[RESOLVED]") {
		t.Fatalf("expected RESOLVED alert, got count=%d msg=%q", msgUC.count(), msgUC.last().Message)
	}
}

func TestHealthWatchdog_DBDownAndBotNotRunningRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"unhealthy","db_ok":false,"expected_connected":2,"actual_connected":0}`))
	}))
	defer srv.Close()

	msgUC := &recordingMsgUC{err: domain.ErrBotNotRunning}
	wd := NewHealthWatchdog(WatchdogConfig{
		TargetURL:     srv.URL,
		FailThreshold: 1,
		AlertUsername: "azhar",
	}, msgUC, zap.NewNop())

	// Bot is not running yet -> SendMessage fails, alerting must remain false.
	wd.CheckOnce(context.Background())
	if msgUC.count() != 0 {
		t.Fatalf("expected 0 sent messages, got %d", msgUC.count())
	}

	// Once bot starts running, next tick successfully delivers the alert.
	msgUC.mu.Lock()
	msgUC.err = nil
	msgUC.mu.Unlock()

	wd.CheckOnce(context.Background())
	if msgUC.count() != 1 || msgUC.last().Username != "azhar" {
		t.Fatalf("expected alert delivery after bot starts running, got count=%d", msgUC.count())
	}
}
