// internal/delivery/http/bid_handler_test.go
package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/gin-gonic/gin"
)

// fakeAuctionUseCase implements only what the bulk-delete handler needs; every
// other method comes from the nil embedded interface and would panic if called.
type fakeAuctionUseCase struct {
	domain.AuctionUseCase

	gotIDs  []uint
	deleted int64
	err     error
}

func (f *fakeAuctionUseCase) DeleteRules(ids []uint) (int64, error) {
	f.gotIDs = append([]uint(nil), ids...)
	return f.deleted, f.err
}

func postBulkDelete(t *testing.T, uc domain.AuctionUseCase, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/rules/bulk-delete", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	NewBidHandler(uc).BulkDeleteRules(c)
	return rec
}

func TestBulkDeleteRulesSuccess(t *testing.T) {
	fake := &fakeAuctionUseCase{deleted: 3}
	rec := postBulkDelete(t, fake, `{"ids":[4,5,6]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got struct {
		Deleted   int64 `json:"deleted"`
		Requested int   `json:"requested"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Deleted != 3 || got.Requested != 3 {
		t.Errorf("got deleted=%d requested=%d, want 3 and 3", got.Deleted, got.Requested)
	}

	if len(fake.gotIDs) != 3 || fake.gotIDs[0] != 4 {
		t.Errorf("usecase received %v, want [4 5 6]", fake.gotIDs)
	}
}

// IDs that were already gone are reported, not treated as a failure.
func TestBulkDeleteRulesReportsPartialDeletion(t *testing.T) {
	rec := postBulkDelete(t, &fakeAuctionUseCase{deleted: 1}, `{"ids":[4,5,6]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got struct {
		Deleted   int64 `json:"deleted"`
		Requested int   `json:"requested"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Deleted != 1 || got.Requested != 3 {
		t.Errorf("got deleted=%d requested=%d, want 1 and 3", got.Deleted, got.Requested)
	}
}

func TestBulkDeleteRulesRejectsMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty array", `{"ids":[]}`},
		{"missing field", `{}`},
		{"wrong type", `{"ids":"1,2,3"}`},
		{"not json", `nope`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAuctionUseCase{}
			rec := postBulkDelete(t, fake, tt.body)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if fake.gotIDs != nil {
				t.Errorf("malformed body must not reach the usecase, got %v", fake.gotIDs)
			}
		})
	}
}

// A rejected selection is the caller's fault (400); anything else is ours (500).
func TestBulkDeleteRulesMapsErrorsToStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"sentinel selection error", domain.ErrInvalidRuleSelection, http.StatusBadRequest},
		{"wrapped sentinel", fmt.Errorf("%w: no rule ids given", domain.ErrInvalidRuleSelection), http.StatusBadRequest},
		// Same text, but not the sentinel: matching must go through errors.Is.
		{"look-alike error is not the sentinel", errors.New(domain.ErrInvalidRuleSelection.Error()), http.StatusInternalServerError},
		{"database failure", errors.New("db down"), http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := postBulkDelete(t, &fakeAuctionUseCase{err: tt.err}, `{"ids":[1]}`)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (body: %s)", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}
