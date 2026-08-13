// internal/usecase/auction_usecase_test.go
package usecase

import (
	"errors"
	"testing"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
)

// fakeBidRepo records the IDs handed to DeleteMany. The embedded interface is nil
// on purpose so any other repository call panics instead of passing silently.
type fakeBidRepo struct {
	domain.BidRepository

	gotIDs  []uint
	deleted int64
	err     error
}

func (f *fakeBidRepo) DeleteMany(ids []uint) (int64, error) {
	f.gotIDs = append([]uint(nil), ids...)
	if f.err != nil {
		return 0, f.err
	}
	return f.deleted, nil
}

func newTestUseCase(repo domain.BidRepository) *auctionUseCase {
	return &auctionUseCase{Repo: repo, Logger: zap.NewNop()}
}

func TestDeleteRulesDeduplicatesAndDropsZeroIDs(t *testing.T) {
	repo := &fakeBidRepo{deleted: 3}
	uc := newTestUseCase(repo)

	deleted, err := uc.DeleteRules([]uint{7, 3, 7, 0, 9, 3})
	if err != nil {
		t.Fatalf("DeleteRules: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	want := []uint{7, 3, 9}
	if len(repo.gotIDs) != len(want) {
		t.Fatalf("repo got %v, want %v", repo.gotIDs, want)
	}
	for i, id := range want {
		if repo.gotIDs[i] != id {
			t.Fatalf("repo got %v, want %v (order preserved)", repo.gotIDs, want)
		}
	}
}

func TestDeleteRulesRejectsEmptySelection(t *testing.T) {
	for _, ids := range [][]uint{nil, {}, {0, 0}} {
		repo := &fakeBidRepo{}
		if _, err := newTestUseCase(repo).DeleteRules(ids); !errors.Is(err, domain.ErrInvalidRuleSelection) {
			t.Errorf("DeleteRules(%v) error = %v, want ErrInvalidRuleSelection", ids, err)
		}
		if repo.gotIDs != nil {
			t.Errorf("DeleteRules(%v) must not reach the repository, got %v", ids, repo.gotIDs)
		}
	}
}

func TestDeleteRulesRejectsOversizedSelection(t *testing.T) {
	ids := make([]uint, maxBulkDeleteIDs+1)
	for i := range ids {
		ids[i] = uint(i + 1)
	}

	repo := &fakeBidRepo{}
	if _, err := newTestUseCase(repo).DeleteRules(ids); !errors.Is(err, domain.ErrInvalidRuleSelection) {
		t.Errorf("error = %v, want ErrInvalidRuleSelection", err)
	}
	if repo.gotIDs != nil {
		t.Error("oversized selection must not reach the repository")
	}
}

// A selection exactly at the cap is still allowed.
func TestDeleteRulesAcceptsSelectionAtCap(t *testing.T) {
	ids := make([]uint, maxBulkDeleteIDs)
	for i := range ids {
		ids[i] = uint(i + 1)
	}

	repo := &fakeBidRepo{deleted: int64(maxBulkDeleteIDs)}
	deleted, err := newTestUseCase(repo).DeleteRules(ids)
	if err != nil {
		t.Fatalf("DeleteRules: %v", err)
	}
	if deleted != int64(maxBulkDeleteIDs) {
		t.Errorf("deleted = %d, want %d", deleted, maxBulkDeleteIDs)
	}
}

func TestDeleteRulesPropagatesRepositoryError(t *testing.T) {
	repoErr := errors.New("db down")
	repo := &fakeBidRepo{err: repoErr}

	deleted, err := newTestUseCase(repo).DeleteRules([]uint{1, 2})
	if !errors.Is(err, repoErr) {
		t.Errorf("error = %v, want %v", err, repoErr)
	}
	if errors.Is(err, domain.ErrInvalidRuleSelection) {
		t.Error("a repository failure must not be reported as a validation error")
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 on error", deleted)
	}
}
