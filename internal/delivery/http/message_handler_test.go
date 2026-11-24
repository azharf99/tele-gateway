// internal/delivery/http/message_handler_test.go
package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type fakeMessageUseCase struct {
	gotReq domain.SendMessageRequest
	err    error
}

func (f *fakeMessageUseCase) SendMessage(_ context.Context, req domain.SendMessageRequest) error {
	f.gotReq = req
	return f.err
}

func signTestJWT(t *testing.T, secret string, role domain.Role) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": float64(1),
		"role":    string(role),
		"name":    "Test",
		"email":   "test@example.com",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

func TestAPIKeyOrAdminMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("JWT_SECRET", "test-secret-123")

	adminJWT := signTestJWT(t, "test-secret-123", domain.RoleAdmin)
	siswaJWT := signTestJWT(t, "test-secret-123", domain.RoleUser)

	setupRouter := func(keys []string) *gin.Engine {
		r := gin.New()
		r.POST("/api/messages/send", APIKeyOrAdminMiddleware(keys), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})
		return r
	}

	tests := []struct {
		name     string
		keys     []string
		apiKey   string
		bearer   string
		wantCode int
	}{
		{
			name:     "valid primary api key",
			keys:     []string{"key-1", "key-2"},
			apiKey:   "key-1",
			wantCode: http.StatusOK,
		},
		{
			name:     "valid rotated secondary api key",
			keys:     []string{"key-1", "key-2"},
			apiKey:   "key-2",
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid api key rejects immediately even with admin jwt",
			keys:     []string{"key-1"},
			apiKey:   "wrong-key",
			bearer:   adminJWT,
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "api key sent when server has no keys configured",
			keys:     nil,
			apiKey:   "key-1",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "admin jwt allowed when no api key header",
			keys:     []string{"key-1"},
			bearer:   adminJWT,
			wantCode: http.StatusOK,
		},
		{
			name:     "siswa jwt rejected with 403",
			keys:     []string{"key-1"},
			bearer:   siswaJWT,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "missing credentials returns 401",
			keys:     []string{"key-1"},
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := setupRouter(tc.keys)
			req := httptest.NewRequest(http.MethodPost, "/api/messages/send", strings.NewReader(`{}`))
			if tc.apiKey != "" {
				req.Header.Set("X-API-Key", tc.apiKey)
			}
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestMessageHandler_SendMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		body     string
		ucErr    error
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"chat_id":123,"topic_id":5,"message":"hello"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "malformed json",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid target error",
			body:     `{"chat_id":123,"message":"hello"}`,
			ucErr:    domain.ErrInvalidTarget,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "target not found error",
			body:     `{"username":"missing","message":"hello"}`,
			ucErr:    domain.ErrTargetNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "account not found error",
			body:     `{"account_id":99,"chat_id":123,"message":"hello"}`,
			ucErr:    domain.ErrAccountNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "account b2b disabled error",
			body:     `{"account_id":2,"chat_id":123,"message":"hello"}`,
			ucErr:    domain.ErrB2BNotEnabled,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "bot not running error",
			body:     `{"chat_id":123,"message":"hello"}`,
			ucErr:    domain.ErrBotNotRunning,
			wantCode: http.StatusServiceUnavailable,
		},
		{
			name:     "unexpected error",
			body:     `{"chat_id":123,"message":"hello"}`,
			ucErr:    errors.New("rpc timeout"),
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeMessageUseCase{err: tc.ucErr}
			h := NewMessageHandler(uc)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/messages/send", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")

			h.SendMessage(c)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
