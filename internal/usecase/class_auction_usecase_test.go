// internal/usecase/class_auction_usecase_test.go
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/parser"
	"github.com/gotd/td/tg"
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
		evNoOv := EvaluateAuction(profile, p, weekly, nil, nil)
		if evNoOv.Matched {
			t.Fatalf("expected SKIPPED without extra override (only 60m free for 90m PREM), got %+v", evNoOv)
		}
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
	mu        sync.Mutex
	profile   *domain.ClassAuctionProfile
	targets   []domain.WatchedTarget
	weekly    []domain.WeeklyAvailability
	overrides []domain.DateAvailabilityOverride
	log       *domain.AuctionLog
	logs      []domain.AuctionLog
	logCh     chan domain.AuctionLog
}

func (f *fakeClassAuctionRepo) GetOrCreateProfile(_ uint) (*domain.ClassAuctionProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *f.profile
	return &cp, nil
}
func (f *fakeClassAuctionRepo) ListWatchedTargets(_ uint) ([]domain.WatchedTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.WatchedTarget(nil), f.targets...), nil
}
func (f *fakeClassAuctionRepo) FindAuctionLogByID(_ uint, _ uint) (*domain.AuctionLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *f.log
	return &cp, nil
}
func (f *fakeClassAuctionRepo) CreateAuctionLog(entry *domain.AuctionLog) error {
	f.mu.Lock()
	f.logs = append(f.logs, *entry)
	ch := f.logCh
	f.mu.Unlock()
	if ch != nil {
		ch <- *entry
	}
	return nil
}
func (f *fakeClassAuctionRepo) UpdateAuctionLogOutcome(_ uint, _ uint, outcome domain.AuctionOutcome) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log.Outcome = outcome
	return nil
}
func (f *fakeClassAuctionRepo) ListWeeklyAvailability(_ uint) ([]domain.WeeklyAvailability, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.WeeklyAvailability(nil), f.weekly...), nil
}
func (f *fakeClassAuctionRepo) ReplaceWeeklyAvailability(_ uint, windows []domain.WeeklyAvailability) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.weekly = append([]domain.WeeklyAvailability(nil), windows...)
	return nil
}
func (f *fakeClassAuctionRepo) ListDateOverrides(_ uint) ([]domain.DateAvailabilityOverride, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.DateAvailabilityOverride(nil), f.overrides...), nil
}
func (f *fakeClassAuctionRepo) CreateDateOverride(ov *domain.DateAvailabilityOverride) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides = append(f.overrides, *ov)
	return nil
}
func (f *fakeClassAuctionRepo) DeleteMatchingBlockOverride(_ uint, date string, startMin, endMin int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.overrides[:0]
	for _, ov := range f.overrides {
		if ov.Date == date && ov.Kind == domain.OverrideBlock && ov.StartMinute == startMin && ov.EndMinute == endMin {
			continue
		}
		kept = append(kept, ov)
	}
	f.overrides = kept
	return nil
}

type fakeTG struct {
	mu      sync.Mutex
	replies []string
	delay   time.Duration
	err     error
}

func (f *fakeTG) Reply(_ context.Context, _ uint, _ tg.InputPeerClass, _ int, text string) error {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.replies = append(f.replies, text)
	return nil
}

func (f *fakeTG) SendSavedMessage(_ context.Context, _ uint, _ string) error { return nil }

func TestMarkLogOutcome_WonCarvesAndLostRestoresSchedule(t *testing.T) {
	t.Run("NEW recurring carves on WON and seamlessly merges back on LOST", func(t *testing.T) {
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
			t.Fatalf("MarkLogOutcome(WON): %v", err)
		}
		// Friday 19:00-22:00 minus 20:30-21:30 => 19:00-20:30 and 21:30-22:00
		if len(repo.weekly) != 2 {
			t.Fatalf("expected 2 split windows, got %+v", repo.weekly)
		}

		// Now revert WON -> LOST: slot [20:30, 21:30) merges cleanly back into [19:00, 22:00)!
		if _, err := uc.MarkLogOutcome(context.Background(), 2, 10, domain.OutcomeLost); err != nil {
			t.Fatalf("MarkLogOutcome(LOST): %v", err)
		}
		if len(repo.weekly) != 1 || repo.weekly[0].StartMinute != 19*60 || repo.weekly[0].EndMinute != 22*60 {
			t.Fatalf("expected restored contiguous [19:00, 22:00), got %+v", repo.weekly)
		}
	})

	t.Run("OTR one-time creates BLOCK on WON and removes BLOCK on LOST", func(t *testing.T) {
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
			t.Fatalf("MarkLogOutcome(WON): %v", err)
		}
		if len(repo.overrides) != 1 || repo.overrides[0].Date != "2026-11-29" || repo.overrides[0].Kind != domain.OverrideBlock || repo.overrides[0].EndMinute != 11*60+30 {
			t.Fatalf("unexpected overrides: %+v", repo.overrides)
		}

		if _, err := uc.MarkLogOutcome(context.Background(), 2, 11, domain.OutcomeLost); err != nil {
			t.Fatalf("MarkLogOutcome(LOST): %v", err)
		}
		if len(repo.overrides) != 0 {
			t.Fatalf("expected BLOCK override removed on LOST, got %+v", repo.overrides)
		}
	})
}

func TestAutoMarkWonOnBid_PreventsSimultaneousDoubleBooking(t *testing.T) {
	prof := domain.DefaultClassAuctionProfile(2)
	prof.DryRun = false
	prof.AutoMarkWonOnBid = true
	prof.NotifySavedMessages = false
	prof.AllowedCourses = []string{"UNITY", "PS1"}

	repo := &fakeClassAuctionRepo{
		profile: prof,
		targets: []domain.WatchedTarget{{AccountID: 2, GroupID: 100, TopicID: 0, IsActive: true}},
		weekly: []domain.WeeklyAvailability{
			{AccountID: 2, DayOfWeek: int(time.Friday), StartMinute: 19 * 60, EndMinute: 22 * 60, IsActive: true},
		},
		logCh: make(chan domain.AuctionLog, 4),
	}

	// Simulate 40ms network RPC latency while 2 different CS posts arrive simultaneously for overlapping Friday evening slots!
	tgMock := &fakeTG{delay: 40 * time.Millisecond}
	uc := NewClassAuctionUseCase(repo, tgMock, nil, zap.NewNop())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = uc.HandleIncomingGroupMessage(context.Background(), 2, 100, 0, 1, false,
			"Need tutor UNITY\nJumat 20.30 WIB", &tg.InputPeerChannel{ChannelID: 100})
	}()
	go func() {
		defer wg.Done()
		// Overlapping slot (20.00-21.00 overlaps 20.30-21.30!) arriving concurrently
		_ = uc.HandleIncomingGroupMessage(context.Background(), 2, 100, 0, 2, false,
			"Need tutor PS1\nJumat 20.00 WIB", &tg.InputPeerChannel{ChannelID: 100})
	}()
	wg.Wait()

	// Wait for both async logs to commit
	var logs []domain.AuctionLog
	for range 2 {
		select {
		case l := <-repo.logCh:
			logs = append(logs, l)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for auction logs")
		}
	}

	tgMock.mu.Lock()
	replyCount := len(tgMock.replies)
	tgMock.mu.Unlock()
	if replyCount != 1 {
		t.Fatalf("sent %d replies for two simultaneous overlapping CS offers, want EXACTLY 1", replyCount)
	}

	wonCount, skippedCount := 0, 0
	for _, l := range logs {
		if l.Decision == domain.DecisionBidSent && l.Outcome == domain.OutcomeWon {
			wonCount++
		}
		if l.Decision == domain.DecisionSkipped {
			skippedCount++
		}
	}
	if wonCount != 1 || skippedCount != 1 {
		t.Fatalf("got won=%d skipped=%d, want won=1 skipped=1 (logs=%+v)", wonCount, skippedCount, logs)
	}

	// Verify DB weekly schedule was automatically carved (split into 2 remaining blocks)
	repo.mu.Lock()
	weeklyLen := len(repo.weekly)
	repo.mu.Unlock()
	if weeklyLen != 2 {
		t.Fatalf("expected weekly schedule to be automatically carved into 2 blocks, got %+v", repo.weekly)
	}
}

func TestAutoMarkWonOnBid_ReleasesReservationOnSendFailureAndSkipsCarveInDryRun(t *testing.T) {
	t.Run("send failure releases reservation so next offer can bid", func(t *testing.T) {
		prof := domain.DefaultClassAuctionProfile(2)
		prof.DryRun = false
		prof.AutoMarkWonOnBid = true
		prof.NotifySavedMessages = false
		prof.AllowedCourses = []string{"UNITY"}

		repo := &fakeClassAuctionRepo{
			profile: prof,
			targets: []domain.WatchedTarget{{AccountID: 2, GroupID: 100, TopicID: 0, IsActive: true}},
			weekly: []domain.WeeklyAvailability{
				{AccountID: 2, DayOfWeek: int(time.Friday), StartMinute: 20*60 + 30, EndMinute: 21*60 + 30, IsActive: true},
			},
			logCh: make(chan domain.AuctionLog, 4),
		}

		tgMock := &fakeTG{err: errors.New("network blip")}
		uc := NewClassAuctionUseCase(repo, tgMock, nil, zap.NewNop())

		// First attempt fails -> must release reservation and NOT carve schedule
		_ = uc.HandleIncomingGroupMessage(context.Background(), 2, 100, 0, 1, false,
			"Need tutor UNITY\nJumat 20.30 WIB", &tg.InputPeerChannel{ChannelID: 100})
		<-repo.logCh

		// Second attempt succeeds -> slot is still free and now wins!
		tgMock.mu.Lock()
		tgMock.err = nil
		tgMock.mu.Unlock()

		_ = uc.HandleIncomingGroupMessage(context.Background(), 2, 100, 0, 2, false,
			"Need tutor UNITY\nJumat 20.30 WIB", &tg.InputPeerChannel{ChannelID: 100})
		l2 := <-repo.logCh
		if l2.Decision != domain.DecisionBidSent || l2.Outcome != domain.OutcomeWon {
			t.Fatalf("expected second offer to win after first send failed, got %+v", l2)
		}
	})

	t.Run("dry run never marks WON or carves schedule", func(t *testing.T) {
		prof := domain.DefaultClassAuctionProfile(2)
		prof.DryRun = true
		prof.AutoMarkWonOnBid = true
		prof.NotifySavedMessages = false
		prof.AllowedCourses = []string{"UNITY"}

		repo := &fakeClassAuctionRepo{
			profile: prof,
			targets: []domain.WatchedTarget{{AccountID: 2, GroupID: 100, TopicID: 0, IsActive: true}},
			weekly: []domain.WeeklyAvailability{
				{AccountID: 2, DayOfWeek: int(time.Friday), StartMinute: 20*60 + 30, EndMinute: 21*60 + 30, IsActive: true},
			},
			logCh: make(chan domain.AuctionLog, 2),
		}

		uc := NewClassAuctionUseCase(repo, &fakeTG{}, nil, zap.NewNop())
		_ = uc.HandleIncomingGroupMessage(context.Background(), 2, 100, 0, 1, false,
			"Need tutor UNITY\nJumat 20.30 WIB", &tg.InputPeerChannel{ChannelID: 100})
		l := <-repo.logCh
		if l.Decision != domain.DecisionDryRunMatch || l.Outcome != domain.OutcomePending {
			t.Fatalf("dry run got decision=%s outcome=%s, want DRY_RUN_MATCH & PENDING", l.Decision, l.Outcome)
		}
		repo.mu.Lock()
		defer repo.mu.Unlock()
		if len(repo.weekly) != 1 {
			t.Fatalf("dry run must not carve schedule, got %+v", repo.weekly)
		}
	})
}
