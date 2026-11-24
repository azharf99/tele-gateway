// internal/delivery/telegram/client_test.go
package telegram

import "testing"

func TestNormalizeMTProtoChatID(t *testing.T) {
	tests := []struct {
		in   int64
		want int64
	}{
		{in: 1234567890, want: 1234567890},
		{in: -1001234567890, want: 1234567890},
		{in: -987654, want: 987654},
		{in: 0, want: 0},
	}

	for _, tc := range tests {
		if got := normalizeMTProtoChatID(tc.in); got != tc.want {
			t.Errorf("normalizeMTProtoChatID(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeUsername(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "@AzharUser", want: "azharuser"},
		{in: "  azhar_99 ", want: "azhar_99"},
		{in: " @my_bot ", want: "my_bot"},
		{in: "", want: ""},
	}

	for _, tc := range tests {
		if got := normalizeUsername(tc.in); got != tc.want {
			t.Errorf("normalizeUsername(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
