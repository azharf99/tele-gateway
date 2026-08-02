// internal/repository/bid_repository_test.go
package repository

import "testing"

// TestMatchPattern_ScheduleRegex covers the multi-line resilience requirement:
// an anchored schedule regex must match a single-line message OR a schedule row
// embedded in a multi-line auction post, but must NOT match a row where the
// schedule is followed by extra text (which breaks the `$` anchor).
func TestMatchPattern_ScheduleRegex(t *testing.T) {
	const pattern = `^(Jadwal\s*:\s*)?(Senin|Monday|Mon)\s+(pukul\s+)?19([.:]00)?\s*(WIB)?$`

	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "single line exact",
			text: "Senin 19.00 WIB",
			want: true,
		},
		{
			name: "multi-line block with matching schedule row",
			text: "Course: python start (usia 12th)\n" +
				"Jadwal: Senin 19.00 WIB\n" +
				"Req: tutor bebas\n" +
				"bahasa: indonesia",
			want: true,
		},
		{
			name: "multi-line block with two schedules on one row must not match",
			text: "Course: python start (usia 12th)\n" +
				"Jadwal: Senin 19.00WIB dan Sabtu 18.00WIB\n" +
				"Req: tutor bebas\n" +
				"bahasa: indonesia",
			want: false,
		},
		{
			name: "unrelated multi-line message",
			text: "Course: python start (usia 12th)\nJadwal: Selasa 20.00 WIB",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchPattern(pattern, tc.text); got != tc.want {
				t.Errorf("matchPattern(pattern, %q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestMatchPattern_SubstringFallback ensures an invalid regex (e.g. "c++",
// whose "++" is an illegal double quantifier) degrades to a case-insensitive
// substring match instead of failing to match anything.
func TestMatchPattern_SubstringFallback(t *testing.T) {
	if !matchPattern("c++", "Course: C++ Programming\nJadwal: Rabu") {
		t.Error("expected invalid-regex pattern to match via case-insensitive substring fallback")
	}
	if matchPattern("golang", "Course: C++ Programming") {
		t.Error("did not expect a substring match for absent keyword")
	}
}
