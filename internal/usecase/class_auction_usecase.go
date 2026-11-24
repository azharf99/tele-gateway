// internal/usecase/class_auction_usecase.go
package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"github.com/azharf99/tele-gateway/internal/parser"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

// AuctionTelegramGateway is the narrow Telegram transport interface needed by ClassAuctionUseCase.
type AuctionTelegramGateway interface {
	Reply(ctx context.Context, accountID uint, peer tg.InputPeerClass, msgID int, text string) error
	SendSavedMessage(ctx context.Context, accountID uint, text string) error
}

type targetKey struct {
	GroupID int64
	TopicID int
}

type accountAuctionCache struct {
	profile   *domain.ClassAuctionProfile
	targets   map[targetKey]*domain.WatchedTarget
	weekly    []domain.WeeklyAvailability
	overrides []domain.DateAvailabilityOverride
}

type classAuctionUseCase struct {
	repo     domain.ClassAuctionRepository
	tg       AuctionTelegramGateway
	aiParser *parser.AIFallbackParser
	logger   *zap.Logger
	now      func() time.Time

	cacheMu sync.RWMutex
	caches  map[uint]*accountAuctionCache

	coolMu    sync.Mutex
	cooldowns map[string]time.Time // key: "accID:day:startMin"
}

func NewClassAuctionUseCase(
	repo domain.ClassAuctionRepository,
	tgGateway AuctionTelegramGateway,
	aiParser *parser.AIFallbackParser,
	logger *zap.Logger,
) domain.ClassAuctionUseCase {
	return &classAuctionUseCase{
		repo:      repo,
		tg:        tgGateway,
		aiParser:  aiParser,
		logger:    logger,
		now:       func() time.Time { return time.Now().In(parser.WIBLocation()) },
		caches:    make(map[uint]*accountAuctionCache),
		cooldowns: make(map[string]time.Time),
	}
}

// SetTelegramGateway allows breaking the circular wiring between AccountManager and ClassAuctionUseCase cleanly in main.go.
func (u *classAuctionUseCase) SetTelegramGateway(tgGateway AuctionTelegramGateway) {
	u.tg = tgGateway
}

func (u *classAuctionUseCase) getOrLoadCache(accountID uint) (*accountAuctionCache, error) {
	u.cacheMu.RLock()
	if c, ok := u.caches[accountID]; ok {
		u.cacheMu.RUnlock()
		return c, nil
	}
	u.cacheMu.RUnlock()

	profile, err := u.repo.GetOrCreateProfile(accountID)
	if err != nil {
		return nil, err
	}
	targets, err := u.repo.ListWatchedTargets(accountID)
	if err != nil {
		return nil, err
	}
	weekly, err := u.repo.ListWeeklyAvailability(accountID)
	if err != nil {
		return nil, err
	}
	overrides, err := u.repo.ListDateOverrides(accountID)
	if err != nil {
		return nil, err
	}

	targetMap := make(map[targetKey]*domain.WatchedTarget, len(targets))
	for i := range targets {
		t := targets[i]
		if !t.IsActive {
			continue
		}
		targetMap[targetKey{GroupID: t.GroupID, TopicID: t.TopicID}] = &t
	}

	c := &accountAuctionCache{
		profile:   profile,
		targets:   targetMap,
		weekly:    weekly,
		overrides: overrides,
	}

	u.cacheMu.Lock()
	u.caches[accountID] = c
	u.cacheMu.Unlock()
	return c, nil
}

func (u *classAuctionUseCase) InvalidateAccountCache(accountID uint) {
	u.cacheMu.Lock()
	delete(u.caches, accountID)
	u.cacheMu.Unlock()
}

func (u *classAuctionUseCase) matchWatchedTarget(c *accountAuctionCache, groupID int64, topicID int) *domain.WatchedTarget {
	if topicID > 0 {
		if t, ok := c.targets[targetKey{GroupID: groupID, TopicID: topicID}]; ok {
			return t
		}
	}
	if t, ok := c.targets[targetKey{GroupID: groupID, TopicID: 0}]; ok {
		return t
	}
	return nil
}

func (u *classAuctionUseCase) isSlotCoolingDown(accountID uint, slot domain.ScheduleSlot) bool {
	u.coolMu.Lock()
	defer u.coolMu.Unlock()
	key := fmt.Sprintf("%d:%d:%d", accountID, slot.DayOfWeek, slot.StartMinute)
	until, ok := u.cooldowns[key]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(u.cooldowns, key)
		return false
	}
	return true
}

func (u *classAuctionUseCase) markSlotsCooldown(accountID uint, slots []domain.ScheduleSlot, cooldownSec int) {
	if cooldownSec <= 0 || len(slots) == 0 {
		return
	}
	until := time.Now().Add(time.Duration(cooldownSec) * time.Second)
	u.coolMu.Lock()
	defer u.coolMu.Unlock()
	for _, s := range slots {
		key := fmt.Sprintf("%d:%d:%d", accountID, s.DayOfWeek, s.StartMinute)
		u.cooldowns[key] = until
	}
}

func (u *classAuctionUseCase) HandleIncomingGroupMessage(
	ctx context.Context,
	accountID uint,
	groupID int64,
	topicID int,
	msgID int,
	isUserReply bool,
	text string,
	peer tg.InputPeerClass,
) error {
	receivedAt := time.Now()

	cache, err := u.getOrLoadCache(accountID)
	if err != nil {
		return fmt.Errorf("load auction cache: %w", err)
	}

	target := u.matchWatchedTarget(cache, groupID, topicID)
	if target == nil {
		return nil // Not a monitored group/topic
	}

	profile := cache.profile
	if !profile.IsActive {
		return nil
	}
	if profile.IgnoreReplies && isUserReply {
		return nil
	}

	parseOpts := parser.ParseOptions{
		DefaultAuctionType: target.DefaultAuctionType,
		StopKeywords:       profile.StopKeywords,
		PrivateDurationMin: profile.PrivateDurationMin,
		GroupDurationMin:   profile.GroupDurationMin,
	}

	nowWIB := u.now()
	parsed := parser.ParseDeterministic(text, nowWIB, parseOpts)
	if !parsed.IsAuctionPost {
		return nil // Regular chatter, skip silently without bloating auction_logs
	}

	parserSource := "DETERMINISTIC"
	if !parsed.HasStopKeyword && parsed.Confidence == domain.ConfidenceLow && profile.EnableAIFallback && u.aiParser != nil {
		if aiParsed, aiErr := u.aiParser.Parse(ctx, text, nowWIB, parseOpts); aiErr == nil && aiParsed != nil && aiParsed.IsAuctionPost {
			parsed = aiParsed
			parserSource = "AI_FALLBACK"
		} else if aiErr != nil && u.logger != nil {
			u.logger.Warn("AI fallback parser failed, keeping deterministic result",
				zap.Uint("account_id", accountID), zap.Error(aiErr))
		}
	}

	eval := EvaluateAuction(profile, parsed, cache.weekly, cache.overrides, func(slot domain.ScheduleSlot) bool {
		return u.isSlotCoolingDown(accountID, slot)
	})

	var sendErr error
	if eval.Matched && !profile.DryRun {
		// Optional delay (defaults to 0ms for instant reply)
		if delay := computeDelay(profile.MinDelayMs, profile.MaxDelayMs); delay > 0 {
			time.Sleep(delay)
		}

		if u.tg == nil {
			sendErr = fmt.Errorf("telegram gateway not configured")
		} else {
			sendErr = u.tg.Reply(ctx, accountID, peer, msgID, eval.ReplyText)
		}

		if sendErr != nil {
			eval.Decision = domain.DecisionError
			eval.SkipReason = "gagal kirim balasan: " + sendErr.Error()
		} else {
			u.markSlotsCooldown(accountID, eval.MatchedSlots, profile.SlotCooldownSec)
		}
	}

	latencyMs := time.Since(receivedAt).Milliseconds()

	if u.logger != nil {
		u.logger.Info("Class auction evaluated",
			zap.Uint("account_id", accountID),
			zap.Int64("group_id", groupID),
			zap.Int("topic_id", topicID),
			zap.Int("msg_id", msgID),
			zap.String("course", string(parsed.Course)),
			zap.String("type", string(parsed.AuctionType)),
			zap.String("decision", string(eval.Decision)),
			zap.String("reply", eval.ReplyText),
			zap.String("skip_reason", eval.SkipReason),
			zap.Int64("latency_ms", latencyMs),
		)
	}

	// Persist log and send Saved Messages notification asynchronously (0ms overhead to bid path)
	parsedBytes, _ := json.Marshal(parsed)
	slotsBytes, _ := json.Marshal(eval.MatchedSlots)
	logEntry := &domain.AuctionLog{
		AccountID:        accountID,
		GroupID:          groupID,
		TopicID:          topicID,
		MessageID:        msgID,
		RawText:          text,
		ParserSource:     parserSource,
		ParseConfidence:  parsed.Confidence,
		ParsedJSON:       string(parsedBytes),
		Decision:         eval.Decision,
		SkipReason:       eval.SkipReason,
		ReplyText:        eval.ReplyText,
		MatchedSlotsJSON: string(slotsBytes),
		LatencyMs:        latencyMs,
		Outcome:          domain.OutcomePending,
	}

	go func() {
		if err := u.repo.CreateAuctionLog(logEntry); err != nil && u.logger != nil {
			u.logger.Error("Failed to save auction log", zap.Uint("account_id", accountID), zap.Error(err))
		}
		if profile.NotifySavedMessages && u.tg != nil && (eval.Decision == domain.DecisionBidSent || eval.Decision == domain.DecisionDryRunMatch) {
			u.notifySavedMessages(accountID, parsed, eval, latencyMs)
		}
	}()

	return sendErr
}

func (u *classAuctionUseCase) notifySavedMessages(accountID uint, parsed *domain.ParsedAuction, eval EvaluationResult, latencyMs int64) {
	prefix := "⚡ [AUTO-BID TERKIRIM]"
	if eval.Decision == domain.DecisionDryRunMatch {
		prefix = "🧪 [DRY-RUN MATCH]"
	}
	slotLabels := make([]string, 0, len(eval.MatchedSlots))
	for _, s := range eval.MatchedSlots {
		lbl := FormatSlotIndonesian(s)
		if s.SpecificDate != "" {
			lbl += " (" + s.SpecificDate + ")"
		}
		slotLabels = append(slotLabels, lbl)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nCourse: %s (%s %dm)\nJenis: %s\nJadwal: %s\nBalasan: \"%s\"\nLatensi: %d ms",
		prefix, parsed.Course, parsed.ClassFormat, parsed.DurationMin, parsed.AuctionType,
		strings.Join(slotLabels, ", "), eval.ReplyText, latencyMs)
	if parsed.LeadURL != "" {
		fmt.Fprintf(&b, "\nLink: %s", parsed.LeadURL)
	}
	if err := u.tg.SendSavedMessage(context.Background(), accountID, b.String()); err != nil && u.logger != nil {
		u.logger.Warn("Failed to send Saved Messages bid notification", zap.Uint("account_id", accountID), zap.Error(err))
	}
}

func computeDelay(minMs, maxMs int) time.Duration {
	if maxMs <= 0 && minMs <= 0 {
		return 0
	}
	if minMs < 0 {
		minMs = 0
	}
	if maxMs < minMs {
		maxMs = minMs
	}
	if maxMs == minMs {
		return time.Duration(minMs) * time.Millisecond
	}
	return time.Duration(minMs+rand.Intn(maxMs-minMs+1)) * time.Millisecond
}

// --- Management API Methods ---

func (u *classAuctionUseCase) GetProfile(accountID uint) (*domain.ClassAuctionProfile, error) {
	return u.repo.GetOrCreateProfile(accountID)
}

func (u *classAuctionUseCase) UpdateProfile(accountID uint, input *domain.ClassAuctionProfile) (*domain.ClassAuctionProfile, error) {
	existing, err := u.repo.GetOrCreateProfile(accountID)
	if err != nil {
		return nil, err
	}

	if input.PrivateDurationMin < 15 || input.PrivateDurationMin > 300 {
		input.PrivateDurationMin = 60
	}
	if input.GroupDurationMin < 15 || input.GroupDurationMin > 300 {
		input.GroupDurationMin = 90
	}
	if input.MinDelayMs < 0 {
		input.MinDelayMs = 0
	}
	if input.MaxDelayMs < input.MinDelayMs {
		input.MaxDelayMs = input.MinDelayMs
	}
	if input.SlotCooldownSec < 0 {
		input.SlotCooldownSec = 0
	}
	if strings.TrimSpace(input.DefaultReply) == "" {
		input.DefaultReply = "Me"
	}
	if strings.TrimSpace(input.GenderNegoReply) == "" {
		input.GenderNegoReply = "Me, nego gender"
	}
	if input.TutorGender != domain.GenderMale && input.TutorGender != domain.GenderFemale {
		input.TutorGender = domain.GenderMale
	}

	existing.IsActive = input.IsActive
	existing.DryRun = input.DryRun
	existing.TutorGender = input.TutorGender
	existing.AllowedCourses = input.AllowedCourses
	existing.GenericDGMatchesAny = input.GenericDGMatchesAny
	existing.AllowedLanguages = input.AllowedLanguages
	existing.AllowedAuctionTypes = input.AllowedAuctionTypes
	existing.AllowedFormats = input.AllowedFormats
	existing.PrivateDurationMin = input.PrivateDurationMin
	existing.GroupDurationMin = input.GroupDurationMin
	existing.AllowGenderNego = input.AllowGenderNego
	existing.RespectNoNego = input.RespectNoNego
	existing.DefaultReply = strings.TrimSpace(input.DefaultReply)
	existing.GenderNegoReply = strings.TrimSpace(input.GenderNegoReply)
	existing.ReplyOverrides = input.ReplyOverrides
	existing.IncludeMatchedSlotOnOR = input.IncludeMatchedSlotOnOR
	existing.MinDelayMs = input.MinDelayMs
	existing.MaxDelayMs = input.MaxDelayMs
	existing.SlotCooldownSec = input.SlotCooldownSec
	existing.EnableAIFallback = input.EnableAIFallback
	existing.IgnoreReplies = input.IgnoreReplies
	existing.StopKeywords = strings.TrimSpace(input.StopKeywords)
	existing.NotifySavedMessages = input.NotifySavedMessages

	if err := u.repo.SaveProfile(existing); err != nil {
		return nil, err
	}
	u.InvalidateAccountCache(accountID)
	return existing, nil
}

func (u *classAuctionUseCase) ListWatchedTargets(accountID uint) ([]domain.WatchedTarget, error) {
	return u.repo.ListWatchedTargets(accountID)
}

func (u *classAuctionUseCase) AddWatchedTarget(accountID uint, target *domain.WatchedTarget) (*domain.WatchedTarget, error) {
	if target.GroupID == 0 {
		return nil, fmt.Errorf("%w: group_id is required", domain.ErrInvalidAuctionConfig)
	}
	if target.TopicID < 0 {
		return nil, fmt.Errorf("%w: topic_id must be >= 0", domain.ErrInvalidAuctionConfig)
	}
	target.AccountID = accountID
	if err := u.repo.CreateWatchedTarget(target); err != nil {
		return nil, err
	}
	u.InvalidateAccountCache(accountID)
	return target, nil
}

func (u *classAuctionUseCase) UpdateWatchedTarget(accountID uint, target *domain.WatchedTarget) (*domain.WatchedTarget, error) {
	target.AccountID = accountID
	if err := u.repo.UpdateWatchedTarget(target); err != nil {
		return nil, err
	}
	u.InvalidateAccountCache(accountID)
	return target, nil
}

func (u *classAuctionUseCase) DeleteWatchedTarget(accountID uint, targetID uint) error {
	if err := u.repo.DeleteWatchedTarget(accountID, targetID); err != nil {
		return err
	}
	u.InvalidateAccountCache(accountID)
	return nil
}

func (u *classAuctionUseCase) GetWeeklySchedule(accountID uint) ([]domain.WeeklyAvailability, error) {
	return u.repo.ListWeeklyAvailability(accountID)
}

func (u *classAuctionUseCase) ReplaceWeeklySchedule(accountID uint, windows []domain.WeeklyAvailability) ([]domain.WeeklyAvailability, error) {
	for i := range windows {
		w := &windows[i]
		if w.DayOfWeek < 0 || w.DayOfWeek > 6 {
			return nil, fmt.Errorf("%w: day_of_week must be 0..6", domain.ErrInvalidAuctionConfig)
		}
		if w.StartMinute < 0 || w.StartMinute >= 1440 || w.EndMinute <= w.StartMinute || w.EndMinute > 1440 {
			return nil, fmt.Errorf("%w: invalid time range %d..%d", domain.ErrInvalidAuctionConfig, w.StartMinute, w.EndMinute)
		}
		w.AccountID = accountID
	}
	if err := u.repo.ReplaceWeeklyAvailability(accountID, windows); err != nil {
		return nil, err
	}
	u.InvalidateAccountCache(accountID)
	return u.repo.ListWeeklyAvailability(accountID)
}

func (u *classAuctionUseCase) ListDateOverrides(accountID uint) ([]domain.DateAvailabilityOverride, error) {
	return u.repo.ListDateOverrides(accountID)
}

func (u *classAuctionUseCase) AddDateOverride(accountID uint, override *domain.DateAvailabilityOverride) (*domain.DateAvailabilityOverride, error) {
	if _, err := time.Parse("2006-01-02", override.Date); err != nil {
		return nil, fmt.Errorf("%w: date must be YYYY-MM-DD", domain.ErrInvalidAuctionConfig)
	}
	if override.Kind != domain.OverrideBlock && override.Kind != domain.OverrideExtra {
		return nil, fmt.Errorf("%w: kind must be BLOCK or EXTRA", domain.ErrInvalidAuctionConfig)
	}
	if override.StartMinute < 0 || override.StartMinute >= 1440 || override.EndMinute <= override.StartMinute || override.EndMinute > 1440 {
		return nil, fmt.Errorf("%w: invalid time range", domain.ErrInvalidAuctionConfig)
	}
	override.AccountID = accountID
	if err := u.repo.CreateDateOverride(override); err != nil {
		return nil, err
	}
	u.InvalidateAccountCache(accountID)
	return override, nil
}

func (u *classAuctionUseCase) DeleteDateOverride(accountID uint, overrideID uint) error {
	if err := u.repo.DeleteDateOverride(accountID, overrideID); err != nil {
		return err
	}
	u.InvalidateAccountCache(accountID)
	return nil
}

func (u *classAuctionUseCase) Simulate(ctx context.Context, accountID uint, req domain.SimulateRequest) (*domain.SimulateResult, error) {
	cache, err := u.getOrLoadCache(accountID)
	if err != nil {
		return nil, err
	}

	nowWIB := u.now()
	if strings.TrimSpace(req.SimulatedNow) != "" {
		if parsedTime, err := parseSimulatedNow(req.SimulatedNow); err == nil {
			nowWIB = parsedTime
		}
	}

	profile := cache.profile
	parseOpts := parser.ParseOptions{
		DefaultAuctionType: req.DefaultAuctionType,
		StopKeywords:       profile.StopKeywords,
		PrivateDurationMin: profile.PrivateDurationMin,
		GroupDurationMin:   profile.GroupDurationMin,
	}

	start := time.Now()
	parsed := parser.ParseDeterministic(req.Text, nowWIB, parseOpts)
	parserSource := "DETERMINISTIC"

	useAI := profile.EnableAIFallback
	if req.UseAIFallback != nil {
		useAI = *req.UseAIFallback
	}
	if parsed.IsAuctionPost && !parsed.HasStopKeyword && parsed.Confidence == domain.ConfidenceLow && useAI && u.aiParser != nil {
		if aiParsed, aiErr := u.aiParser.Parse(ctx, req.Text, nowWIB, parseOpts); aiErr == nil && aiParsed != nil {
			parsed = aiParsed
			parserSource = "AI_FALLBACK"
		}
	}
	elapsed := time.Since(start)

	eval := EvaluateAuction(profile, parsed, cache.weekly, cache.overrides, nil)
	return &domain.SimulateResult{
		ParserSource:  parserSource,
		ParseDuration: elapsed.String(),
		ParseMicros:   elapsed.Microseconds(),
		Parsed:        parsed,
		WouldBid:      eval.Matched,
		Decision:      eval.Decision,
		SkipReason:    eval.SkipReason,
		ReplyText:     eval.ReplyText,
		NeedsNego:     eval.NeedsGenderNego,
		MatchedSlots:  eval.MatchedSlots,
	}, nil
}

func parseSimulatedNow(raw string) (time.Time, error) {
	loc := parser.WIBLocation()
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(raw), loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported simulated_now format")
}

func (u *classAuctionUseCase) ListLogs(accountID uint, filter domain.AuctionLogFilter) (*domain.AuctionLogPage, error) {
	return u.repo.ListAuctionLogs(accountID, filter)
}

// MarkLogOutcome updates WON/LOST on an auction log. When marked WON, it automatically
// carves out the won slot(s) from WeeklyAvailability (for recurring NEW/FR classes)
// or adds a DateAvailabilityOverride BLOCK (for one-time OTR/CU classes).
func (u *classAuctionUseCase) MarkLogOutcome(ctx context.Context, accountID uint, logID uint, outcome domain.AuctionOutcome) (*domain.AuctionLog, error) {
	if outcome != domain.OutcomeWon && outcome != domain.OutcomeLost && outcome != domain.OutcomePending {
		return nil, fmt.Errorf("%w: invalid outcome %q", domain.ErrInvalidAuctionConfig, outcome)
	}

	entry, err := u.repo.FindAuctionLogByID(accountID, logID)
	if err != nil {
		return nil, err
	}

	wasWon := entry.Outcome == domain.OutcomeWon
	if err := u.repo.UpdateAuctionLogOutcome(accountID, logID, outcome); err != nil {
		return nil, err
	}
	entry.Outcome = outcome

	if outcome == domain.OutcomeWon && !wasWon && entry.Parsed != nil && len(entry.MatchedSlots) > 0 {
		duration := entry.Parsed.DurationMin
		if duration <= 0 {
			duration = 60
		}

		isOneTime := entry.Parsed.AuctionType == domain.AuctionTypeOTR || entry.Parsed.AuctionType == domain.AuctionTypeCU
		if isOneTime {
			for _, slot := range entry.MatchedSlots {
				dateStr := slot.SpecificDate
				if dateStr == "" {
					dateStr = nextDateForWeekday(entry.CreatedAt.In(parser.WIBLocation()), slot.DayOfWeek)
				}
				endMin := slot.StartMinute + duration
				if endMin > 1440 {
					endMin = 1440
				}
				_ = u.repo.CreateDateOverride(&domain.DateAvailabilityOverride{
					AccountID:   accountID,
					Date:        dateStr,
					Kind:        domain.OverrideBlock,
					StartMinute: slot.StartMinute,
					EndMinute:   endMin,
					Reason:      fmt.Sprintf("Menang %s %s", entry.Parsed.AuctionType, entry.Parsed.Course),
				})
			}
		} else {
			// Recurring class (NEW or FR): carve slot(s) out of WeeklyAvailability
			weekly, err := u.repo.ListWeeklyAvailability(accountID)
			if err == nil {
				updated := carveWeeklyAvailability(accountID, weekly, entry.MatchedSlots, duration)
				_ = u.repo.ReplaceWeeklyAvailability(accountID, updated)
			}
		}
		u.InvalidateAccountCache(accountID)
	}

	return entry, nil
}

func carveWeeklyAvailability(accountID uint, weekly []domain.WeeklyAvailability, slots []domain.ScheduleSlot, duration int) []domain.WeeklyAvailability {
	byDay := make(map[int][]TimeRange)
	var inactive []domain.WeeklyAvailability
	for _, w := range weekly {
		if !w.IsActive {
			inactive = append(inactive, w)
			continue
		}
		byDay[w.DayOfWeek] = append(byDay[w.DayOfWeek], TimeRange{Start: w.StartMinute, End: w.EndMinute})
	}
	for _, slot := range slots {
		endMin := slot.StartMinute + duration
		if endMin > 1440 {
			endMin = 1440
		}
		byDay[slot.DayOfWeek] = SubtractTimeRange(byDay[slot.DayOfWeek], TimeRange{
			Start: slot.StartMinute,
			End:   endMin,
		})
	}
	var result []domain.WeeklyAvailability
	for day := 0; day < 7; day++ {
		for _, r := range MergeTimeRanges(byDay[day]) {
			result = append(result, domain.WeeklyAvailability{
				AccountID:   accountID,
				DayOfWeek:   day,
				StartMinute: r.Start,
				EndMinute:   r.End,
				IsActive:    true,
			})
		}
	}
	result = append(result, inactive...)
	return result
}

func nextDateForWeekday(ref time.Time, targetWeekday int) string {
	diff := (targetWeekday - int(ref.Weekday()) + 7) % 7
	return ref.AddDate(0, 0, diff).Format("2006-01-02")
}

func (u *classAuctionUseCase) GetStats(accountID uint) (*domain.AuctionStats, error) {
	return u.repo.GetStats(accountID)
}
