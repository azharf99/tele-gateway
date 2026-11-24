// internal/delivery/telegram/manager_test.go
package telegram

import (
	"errors"
	"testing"

	"github.com/azharf99/tele-gateway/internal/domain"
)

func TestAccountManager_ResolveB2BAccount(t *testing.T) {
	mgr := &AccountManager{
		runtimes: map[uint]*accountRuntime{
			1: {
				account: domain.TelegramAccount{ID: 1, Name: "B2B Primary", EnableB2B: true, IsDefaultB2B: true},
				state:   domain.AccountRuntimeState{Status: domain.AccountStatusRunning},
			},
			2: {
				account: domain.TelegramAccount{ID: 2, Name: "Algonova Lelang", EnableB2B: false, EnableClassAuction: true},
				state:   domain.AccountRuntimeState{Status: domain.AccountStatusRunning},
			},
			3: {
				account: domain.TelegramAccount{ID: 3, Name: "B2B Secondary", EnableB2B: true, IsDefaultB2B: false},
				state:   domain.AccountRuntimeState{Status: domain.AccountStatusStopped},
			},
		},
	}

	t.Run("default account (0) resolves to IsDefaultB2B account", func(t *testing.T) {
		id, status, err := mgr.ResolveB2BAccount(0)
		if err != nil || id != 1 || status != domain.AccountStatusRunning {
			t.Fatalf("got (%d, %s, %v), want (1, RUNNING, nil)", id, status, err)
		}
	})

	t.Run("explicit non-B2B auction account is rejected", func(t *testing.T) {
		_, _, err := mgr.ResolveB2BAccount(2)
		if !errors.Is(err, domain.ErrB2BNotEnabled) {
			t.Fatalf("got err = %v, want ErrB2BNotEnabled", err)
		}
	})

	t.Run("explicit secondary B2B account resolves with its status", func(t *testing.T) {
		id, status, err := mgr.ResolveB2BAccount(3)
		if err != nil || id != 3 || status != domain.AccountStatusStopped {
			t.Fatalf("got (%d, %s, %v), want (3, STOPPED, nil)", id, status, err)
		}
	})

	t.Run("unknown account returns ErrAccountNotFound", func(t *testing.T) {
		_, _, err := mgr.ResolveB2BAccount(99)
		if !errors.Is(err, domain.ErrAccountNotFound) {
			t.Fatalf("got err = %v, want ErrAccountNotFound", err)
		}
	})
}
