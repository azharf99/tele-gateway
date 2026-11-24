// internal/usecase/class_auction_usecase_test.go
package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/parser"
	"go.uber.org/zap"
)

func TestMergeAndSubtractTimeRanges(t *testing.T) {
	merged := MergeTimeRanges([]TimeRange{
		{Start: 19 * 60, End: 20 * 60},
		{Start: 20 * 60, End: 21 * 60},
		{Start: 10 * 60, End: 12 * 60},
	})
	if len(merged) != 2 {
		t.Fatalf("len(merged) = %d, want 2 (%+v)", len(merged), merged)
	}
	if merged[0] != (TimeRange{10 * 60, 12 * 60}) || merged[1] != (TimeRange{19 * 60, 21 * 60}) {
		t.Fatalf("unexpected merged: %+v", merged)
	}

	// Carve middle [19:30, 20:30) from [19:00, 22:00) -> [19:00, 19:30) and [20:30, 22:00)
	carved := SubtractTimeRange([]TimeRange{{19 * 60, 22 * 60}}, TimeRange{19*60 + 30, 20*60 + 30})
	if len(carved) != 2 {
		t.Fatalf("len(carved) = %d, want 2 (%+v)", len(carved), carved)
	}
	if carved[0] != (TimeRange{19 * 60, 19*60 + 30}) || carved[1] != (TimeRange{20*60 + 30, 22 * 60}) {
		t.Fatalf("unexpected carved: %+v", carved)
	}

	// Exact removal
	empty := SubtractTimeRange([]TimeRange{{19 * 60, 20 * 60}}, TimeRange{19 * 60, 20 * 60})
	if len(empty) != 0 {
		t.Fatalf("expected empty after exact removal, got %+v", empty)
	}
}

func TestEvaluateAuction_MultiSlotAND_OR_GenderNego_Duration_Overrides(t *testing.T) {
	profile := domain.DefaultClassAuctionProfile(2)
	profile.DryRun = false
	profile.TutorGender = domain.GenderMale
	profile.AllowedCourses = []string{"UNITY", "VP", "PS2", "DGJ", "DL"}
	profile.AllowedLanguages = []string{"INDONESIA", "BILINGUAL", "FULL_ENGLISH"}
	profile.DefaultReply = "Me"
	profile.GenderNegoReply = "Me, nego gender"
	profile.IncludeMatchedSlotOnOR = true

	weekly := []domain.WeeklyAvailability{
		{DayOfWeek: int(time.Friday), StartMinute: 20*60 + 30, EndMinute: 22 * 60, IsActive: true},
		{DayOfWeek: int(time.Saturday), StartMinute: 12 * 60, EndMinute: 14 * 60, IsActive: true},
		{DayOfWeek: int(time.Saturday), StartMinute: 19 * 60, EndMinute: 21 * 60, IsActive: true},
		{DayOfWeek: int(time.Sunday), StartMinute: 10 * 60, EndMinute: 11 * 60, IsActive: true}, // Only 60m! Not enough for 90m PREM
		{DayOfWeek: int(time.Wednesday), StartMinute: 18 * 60, EndMinute: 20 * 60, IsActive: true},
	}

	overrides := []domain.DateAvailabilityOverride{
		// Extra 90m window on 2026-11-29 (Sunday) for OTR PS2 PREM
		{Date: "2026-11-29", Kind: domain.OverrideExtra, StartMinute: 10 * 60, EndMinute: 12 * 60},
		// Block Friday 2026-11-27
		{Date: "2026-11-27", Kind: domain.OverrideBlock, StartMinute: 0, EndMinute: 1440},
	}

	now := time.Date(2026, time.November, 24, 10, 0, 0, 0, parser.WIBLocation())

	t.Run("AND two slots both fit", func(t *testing.T) {
		p := parser.ParseDeterministic("Need tutor UNITY\n(FRI 20.30, SAT 12.00)", now, parser.ParseOptions{})
		ev := EvaluateAuction(profile, p, weekly, overrides, nil)
		if !ev.Matched || ev.Decision != domain.DecisionBidSent || ev.ReplyText != "Me" {
			t.Fatalf("got %+v, want BID_SENT with 'Me'", ev)
		}
	})

	t.Run("AND fails if one slot missing", func(t *testing.T) {
		p := parser.ParseDeterministic("Need tutor UNITY\n(FRI 20.30, MON 12.00)", now, parser.ParseOptions{})
		ev := EvaluateAuction(profile, p, weekly, overrides, nil)
		if ev.Matched || ev.Decision != domain.DecisionSkipped {
			t.Fatalf("expected SKIPPED, got %+v", ev)
		}
	})

	t.Run("OR matches Saturday 19:00 and appends gender nego + chosen slot", func(t *testing.T) {
		p := parser.ParseDeterministic("NEED TUTOR PRIVATE\nVP\nJumat atau Sabtu pkl. 19:00 WIB\nPerempuan\nFull English", now, parser.ParseOptions{})
		ev := EvaluateAuction(profile, p, weekly, overrides, nil)
		if !ev.Matched || !ev.NeedsGenderNego {
			t.Fatalf("expected match with gender nego, got %+v", ev)
		}
		if ev.ReplyText != "Me, nego gender, Sabtu 19:00" {
			t.Fatalf("ReplyText = %q, want 'Me, nego gender, Sabtu 19:00'", ev.ReplyText)
		}
	})

	t.Run("90-min PREM rejected when weekly only has 60m, accepted via EXTRA date override", func(t *testing.T) {
		p := parser.ParseDeterministic("NEED OTR CUTI\nPS2 PREM\nMinggu, 29 Nov 2026 Pukul 10.00", now, parser.ParseOptions{})
		// Without override -> 60m weekly is too short for 90m PREM
		evNoOv := EvaluateAuction(profile, p, weekly, nil, nil)
		if evNoOv.Matched {
			t.Fatalf("expected SKIPPED without extra override (only 60m free for 90m PREM), got %+v", evNoOv)
		}
		// With EXTRA override on 2026-11-29 -> fits!
		evWithOv := EvaluateAuction(profile, p, weekly, overrides, nil)
		if !evWithOv.Matched {
			t.Fatalf("expected BID_SENT with EXTRA override, got %+v", evWithOv)
		}
	})

	t.Run("NO NEGO blocks gender negotiation", func(t *testing.T) {
		p := parser.ParseDeterministic("NEED TUTOR FOREVER REPLACEMENT\nIND 1-1 DL 413 (WED 18.30) <— NO NEGO\nTutor Perempuan\nBahasa Indonesia", now, parser.ParseOptions{})
		ev := EvaluateAuction(profile, p, weekly, overrides, nil)
		if ev.Matched || ev.Decision != domain.DecisionSkipped {
			t.Fatalf("expected SKIPPED due to NO NEGO gender mismatch, got %+v", ev)
		}
	})

	t.Run("Language mismatch skips", func(t *testing.T) {
		profIndoOnly := *profile
		profIndoOnly.AllowedLanguages = []string{"INDONESIA"}
		p := parser.ParseDeterministic("Need tutor UNITY\n(FRI 20.30, SAT 12.00)\nFull English", now, parser.ParseOptions{})
		ev := EvaluateAuction(&profIndoOnly, p, weekly, overrides, nil)
		if ev.Matched || ev.Decision != domain.DecisionSkipped {
			t.Fatalf("expected SKIPPED on language mismatch, got %+v", ev)
		}
	})
}

type fakeClassAuctionRepo struct {
	domain.ClassAuctionRepository
	weekly    []domain.WeeklyAvailability
	overrides []domain.DateAvailabilityOverride
	log       *domain.AuctionLog
}

func (f *fakeClassAuctionRepo) FindAuctionLogByID(_ uint, _ uint) (*domain.AuctionLog, error) {
	return f.log, nil
}
func (f *fakeClassAuctionRepo) UpdateAuctionLogOutcome(_ uint, _ uint, outcome domain.AuctionOutcome) error {
	f.log.Outcome = outcome
	return nil
}
func (f *fakeClassAuctionRepo) ListWeeklyAvailability(_ uint) ([]domain.WeeklyAvailability, error) {
	return f.weekly, nil
}
func (f *fakeClassAuctionRepo) ReplaceWeeklyAvailability(_ uint, windows []domain.WeeklyAvailability) error {
	f.weekly = windows
	return nil
}
func (f *fakeClassAuctionRepo) CreateDateOverride(ov *domain.DateAvailabilityOverride) error {
	f.overrides = append(f.overrides, *ov)
	return nil
}

func TestMarkLogOutcome_WonCarvesRecurringAndBlocksOneTime(t *testing.T) {
	t.Run("NEW recurring carves WeeklyAvailability", func(t *testing.T) {
		parsed := &domain.ParsedAuction{Course: domain.CourseUnity, AuctionType: domain.AuctionTypeNew, DurationMin: 60}
		slots := []domain.ScheduleSlot{{DayOfWeek: int(time.Friday), StartMinute: 20*60 + 30}}
		pBytes, _ := json.Marshal(parsed)
		sBytes, _ := json.Marshal(slots)

		repo := &fakeClassAuctionRepo{
			weekly: []domain.WeeklyAvailability{
				{AccountID: 2, DayOfWeek: int(time.Friday), StartMinute: 19 * 60, EndMinute: 22 * 60, IsActive: true},
			},
			log: &domain.AuctionLog{
				ID:               10,
				AccountID:        2,
				ParsedJSON:       string(pBytes),
				Parsed:           parsed,
				MatchedSlotsJSON: string(sBytes),
				MatchedSlots:     slots,
				Outcome:          domain.OutcomePending,
			},
		}

		uc := NewClassAuctionUseCase(repo, nil, nil, zap.NewNop())
		if _, err := uc.MarkLogOutcome(context.Background(), 2, 10, domain.OutcomeWon); err != nil {
			t.Fatalf("MarkLogOutcome: %v", err)
		}
		// Friday 19:00-22:00 minus 20:30-21:30 => 19:00-20:30 and 21:30-22:00
		if len(repo.weekly) != 2 {
			t.Fatalf("expected 2 split windows, got %+v", repo.weekly)
		}
		if repo.weekly[0].StartMinute != 19*60 || repo.weekly[0].EndMinute != 20*60+30 ||
			repo.weekly[1].StartMinute != 21*60+30 || repo.weekly[1].EndMinute != 22*60 {
			t.Fatalf("unexpected carved windows: %+v", repo.weekly)
		}
	})

	t.Run("OTR one-time creates BLOCK DateAvailabilityOverride", func(t *testing.T) {
		parsed := &domain.ParsedAuction{Course: domain.CoursePS2, AuctionType: domain.AuctionTypeOTR, DurationMin: 90}
		slots := []domain.ScheduleSlot{{DayOfWeek: int(time.Sunday), StartMinute: 10 * 60, SpecificDate: "2026-11-29"}}

		repo := &fakeClassAuctionRepo{
			log: &domain.AuctionLog{
				ID:           11,
				AccountID:    2,
				Parsed:       parsed,
				MatchedSlots: slots,
				Outcome:      domain.OutcomePending,
			},
		}

		uc := NewClassAuctionUseCase(repo, nil, nil, zap.NewNop())
		if _, err := uc.MarkLogOutcome(context.Background(), 2, 11, domain.OutcomeWon); err != nil {
			t.Fatalf("MarkLogOutcome: %v", err)
		}
		if len(repo.overrides) != 1 || repo.overrides[0].Date != "2026-11-29" || repo.overrides[0].Kind != domain.OverrideBlock || repo.overrides[0].EndMinute != 11*60+30 {
			t.Fatalf("unexpected overrides: %+v", repo.overrides)
		}
	})
}
