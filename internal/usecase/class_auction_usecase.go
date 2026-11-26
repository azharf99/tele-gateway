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

type slotReservation struct {
	ID           uint64
	AccountID    uint
	DayOfWeek    int
	SpecificDate string // non-empty for 1-time OTR/CU reservations
	IsOneTime    bool
	StartMinute  int
	EndMinute    int
}

type classAuctionUseCase struct {
	repo     domain.ClassAuctionRepository
	tg       AuctionTelegramGateway
	aiParser *parser.AIFallbackParser
	logger   *zap.Logger
	now      func() time.Time

	cacheMu sync.RWMutex
	caches  map[uint]*accountAuctionCache

	// reserveMu serializes slot evaluation + reservation per auction so two
	// simultaneous CS messages for overlapping slots can never both fire a bid.
	reserveMu    sync.Mutex
	nextResID    uint64
	reservations map[uint][]slotReservation

	// scheduleWriteMu serializes read-modify-write schedule carves/restores so
	// two concurrent non-overlapping bids can never clobber each other's DB write.
	scheduleWriteMu sync.Mutex

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
		repo:         repo,
		tg:           tgGateway,
		aiParser:     aiParser,
		logger:       logger,
		now:          func() time.Time { return time.Now().In(parser.WIBLocation()) },
		caches:       make(map[uint]*accountAuctionCache),
		reservations: make(map[uint][]slotReservation),
		cooldowns:    make(map[string]time.Time),
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

	// Atomically evaluate against current availability MINUS any in-flight reservations
	// and reserve matched slots BEFORE sending the Telegram reply so simultaneous CS
	// offers for the same/overlapping time slot can never double-book.
	eval, resIDs, normalizedSlots := u.evaluateAndReserve(accountID, profile, parsed, cache, nowWIB)

	var sendErr error
	outcome := domain.OutcomePending
	autoWon := false

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
			u.releaseReservations(accountID, resIDs)
			eval.Decision = domain.DecisionError
			eval.SkipReason = "gagal kirim balasan: " + sendErr.Error()
		} else {
			u.markSlotsCooldown(accountID, eval.MatchedSlots, profile.SlotCooldownSec)
			if profile.AutoMarkWonOnBid {
				outcome = domain.OutcomeWon
				autoWon = true
			}
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
			zap.String("outcome", string(outcome)),
			zap.String("reply", eval.ReplyText),
			zap.String("skip_reason", eval.SkipReason),
			zap.Int64("latency_ms", latencyMs),
		)
	}

	// Persist carved schedule (if autoWon), log, and send Saved Messages notification
	// asynchronously so reply latency stays ~0ms. In-flight reservations remain held until
	// the DB schedule update & cache invalidation complete — zero race window!
	parsedBytes, _ := json.Marshal(parsed)
	slotsToSave := eval.MatchedSlots
	if len(normalizedSlots) > 0 {
		slotsToSave = normalizedSlots
	}
	slotsBytes, _ := json.Marshal(slotsToSave)
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
		Outcome:          outcome,
	}

	go func() {
		if autoWon && len(slotsToSave) > 0 {
			u.applyWonSchedule(accountID, parsed, slotsToSave, nowWIB)
			u.InvalidateAccountCache(accountID)
			u.releaseReservations(accountID, resIDs)
		}
		if err := u.repo.CreateAuctionLog(logEntry); err != nil && u.logger != nil {
			u.logger.Error("Failed to save auction log", zap.Uint("account_id", accountID), zap.Error(err))
		}
		if profile.NotifySavedMessages && u.tg != nil && (eval.Decision == domain.DecisionBidSent || eval.Decision == domain.DecisionDryRunMatch) {
			u.notifySavedMessages(accountID, parsed, eval, autoWon, latencyMs)
		}
	}()

	return sendErr
}

// evaluateAndReserve runs EvaluateAuction with any active in-flight reservations
// subtracted from availability, and atomically records reservations for the matched
// slots when live AutoMarkWonOnBid is active.
func (u *classAuctionUseCase) evaluateAndReserve(
	accountID uint,
	profile *domain.ClassAuctionProfile,
	parsed *domain.ParsedAuction,
	cache *accountAuctionCache,
	nowWIB time.Time,
) (EvaluationResult, []uint64, []domain.ScheduleSlot) {
	u.reserveMu.Lock()
	defer u.reserveMu.Unlock()

	effectiveWeekly, effectiveOverrides := u.applyInFlightReservations(accountID, cache.weekly, cache.overrides)

	// Normalize specific date on 1-time OTR/CU slots so date overrides & reservations always align
	isOneTime := parsed.AuctionType == domain.AuctionTypeOTR || parsed.AuctionType == domain.AuctionTypeCU
	evalParsed := *parsed
	if isOneTime && len(parsed.Schedules) > 0 {
		evalParsed.Schedules = make([]domain.ScheduleSlot, len(parsed.Schedules))
		copy(evalParsed.Schedules, parsed.Schedules)
		for i := range evalParsed.Schedules {
			if evalParsed.Schedules[i].SpecificDate == "" {
				evalParsed.Schedules[i].SpecificDate = nextDateForWeekday(nowWIB, evalParsed.Schedules[i].DayOfWeek)
			}
		}
	}

	eval := EvaluateAuction(profile, &evalParsed, effectiveWeekly, effectiveOverrides, func(slot domain.ScheduleSlot) bool {
		return u.isSlotCoolingDown(accountID, slot)
	})

	if !eval.Matched || profile.DryRun || !profile.AutoMarkWonOnBid || len(eval.MatchedSlots) == 0 {
		return eval, nil, eval.MatchedSlots
	}

	duration := resolvedDuration(profile, &evalParsed)
	resIDs := make([]uint64, 0, len(eval.MatchedSlots))
	for _, slot := range eval.MatchedSlots {
		endMin := slot.StartMinute + duration
		if endMin > 1440 {
			endMin = 1440
		}
		u.nextResID++
		id := u.nextResID
		u.reservations[accountID] = append(u.reservations[accountID], slotReservation{
			ID:           id,
			AccountID:    accountID,
			DayOfWeek:    slot.DayOfWeek,
			SpecificDate: slot.SpecificDate,
			IsOneTime:    isOneTime,
			StartMinute:  slot.StartMinute,
			EndMinute:    endMin,
		})
		resIDs = append(resIDs, id)
	}

	return eval, resIDs, eval.MatchedSlots
}

func (u *classAuctionUseCase) applyInFlightReservations(
	accountID uint,
	weekly []domain.WeeklyAvailability,
	overrides []domain.DateAvailabilityOverride,
) ([]domain.WeeklyAvailability, []domain.DateAvailabilityOverride) {
	active := u.reservations[accountID]
	if len(active) == 0 {
		return weekly, overrides
	}

	effWeekly := append([]domain.WeeklyAvailability(nil), weekly...)
	effOverrides := append([]domain.DateAvailabilityOverride(nil), overrides...)

	for _, res := range active {
		if res.IsOneTime && res.SpecificDate != "" {
			effOverrides = append(effOverrides, domain.DateAvailabilityOverride{
				AccountID:   accountID,
				Date:        res.SpecificDate,
				Kind:        domain.OverrideBlock,
				StartMinute: res.StartMinute,
				EndMinute:   res.EndMinute,
			})
		} else {
			effWeekly = carveWeeklyAvailability(accountID, effWeekly, []domain.ScheduleSlot{
				{DayOfWeek: res.DayOfWeek, StartMinute: res.StartMinute},
			}, res.EndMinute-res.StartMinute)
		}
	}
	return effWeekly, effOverrides
}

func (u *classAuctionUseCase) releaseReservations(accountID uint, ids []uint64) {
	if len(ids) == 0 {
		return
	}
	drop := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		drop[id] = struct{}{}
	}
	u.reserveMu.Lock()
	defer u.reserveMu.Unlock()
	cur := u.reservations[accountID]
	if len(cur) == 0 {
		return
	}
	kept := cur[:0]
	for _, r := range cur {
		if _, rm := drop[r.ID]; !rm {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		delete(u.reservations, accountID)
	} else {
		u.reservations[accountID] = kept
	}
}

func resolvedDuration(profile *domain.ClassAuctionProfile, parsed *domain.ParsedAuction) int {
	if parsed != nil && parsed.DurationMin > 0 {
		return parsed.DurationMin
	}
	if parsed != nil && parsed.ClassFormat == domain.ClassFormatGroup {
		if profile != nil && profile.GroupDurationMin > 0 {
			return profile.GroupDurationMin
		}
		return 90
	}
	if profile != nil && profile.PrivateDurationMin > 0 {
		return profile.PrivateDurationMin
	}
	return 60
}

func (u *classAuctionUseCase) applyWonSchedule(accountID uint, parsed *domain.ParsedAuction, slots []domain.ScheduleSlot, refTime time.Time) {
	if parsed == nil || len(slots) == 0 {
		return
	}
	u.scheduleWriteMu.Lock()
	defer u.scheduleWriteMu.Unlock()

	duration := parsed.DurationMin
	if duration <= 0 {
		if parsed.ClassFormat == domain.ClassFormatGroup {
			duration = 90
		} else {
			duration = 60
		}
	}

	isOneTime := parsed.AuctionType == domain.AuctionTypeOTR || parsed.AuctionType == domain.AuctionTypeCU
	if isOneTime {
		for _, slot := range slots {
			dateStr := slot.SpecificDate
			if dateStr == "" {
				dateStr = nextDateForWeekday(refTime, slot.DayOfWeek)
			}
			endMin := slot.StartMinute + duration
			if endMin > 1440 {
				endMin = 1440
			}
			if err := u.repo.CreateDateOverride(&domain.DateAvailabilityOverride{
				AccountID:   accountID,
				Date:        dateStr,
				Kind:        domain.OverrideBlock,
				StartMinute: slot.StartMinute,
				EndMinute:   endMin,
				Reason:      fmt.Sprintf("Menang %s %s", parsed.AuctionType, parsed.Course),
			}); err != nil && u.logger != nil {
				u.logger.Error("Failed to create BLOCK override for won one-time class", zap.Uint("account_id", accountID), zap.Error(err))
			}
		}
		return
	}

	weekly, err := u.repo.ListWeeklyAvailability(accountID)
	if err != nil {
		if u.logger != nil {
			u.logger.Error("Failed to list weekly availability for carving", zap.Uint("account_id", accountID), zap.Error(err))
		}
		return
	}
	updated := carveWeeklyAvailability(accountID, weekly, slots, duration)
	if err := u.repo.ReplaceWeeklyAvailability(accountID, updated); err != nil && u.logger != nil {
		u.logger.Error("Failed to persist carved weekly availability", zap.Uint("account_id", accountID), zap.Error(err))
	}
}

func (u *classAuctionUseCase) restoreWonSchedule(accountID uint, parsed *domain.ParsedAuction, slots []domain.ScheduleSlot, refTime time.Time) {
	if parsed == nil || len(slots) == 0 {
		return
	}
	u.scheduleWriteMu.Lock()
	defer u.scheduleWriteMu.Unlock()

	duration := parsed.DurationMin
	if duration <= 0 {
		if parsed.ClassFormat == domain.ClassFormatGroup {
			duration = 90
		} else {
			duration = 60
		}
	}

	isOneTime := parsed.AuctionType == domain.AuctionTypeOTR || parsed.AuctionType == domain.AuctionTypeCU
	if isOneTime {
		for _, slot := range slots {
			dateStr := slot.SpecificDate
			if dateStr == "" {
				dateStr = nextDateForWeekday(refTime, slot.DayOfWeek)
			}
			endMin := slot.StartMinute + duration
			if endMin > 1440 {
				endMin = 1440
			}
			if err := u.repo.DeleteMatchingBlockOverride(accountID, dateStr, slot.StartMinute, endMin); err != nil && u.logger != nil {
				u.logger.Error("Failed to remove BLOCK override when restoring lost class", zap.Uint("account_id", accountID), zap.Error(err))
			}
		}
		return
	}

	weekly, err := u.repo.ListWeeklyAvailability(accountID)
	if err != nil {
		return
	}
	restored := restoreWeeklyAvailability(accountID, weekly, slots, duration)
	if err := u.repo.ReplaceWeeklyAvailability(accountID, restored); err != nil && u.logger != nil {
		u.logger.Error("Failed to restore weekly availability", zap.Uint("account_id", accountID), zap.Error(err))
	}
}

func (u *classAuctionUseCase) notifySavedMessages(accountID uint, parsed *domain.ParsedAuction, eval EvaluationResult, autoWon bool, latencyMs int64) {
	prefix := "⚡ [AUTO-BID TERKIRIM]"
	if autoWon {
		prefix = "⚡🏆 [AUTO-BID & OTOMATIS MENANG]"
	} else if eval.Decision == domain.DecisionDryRunMatch {
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
	if autoWon {
		b.WriteString("\nStatus: Jadwal otomatis terpotong (klik Kalah di Web UI jika ingin mengembalikan slot).")
	}
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
	existing.AutoMarkWonOnBid = input.AutoMarkWonOnBid
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
	u.scheduleWriteMu.Lock()
	err := u.repo.ReplaceWeeklyAvailability(accountID, windows)
	u.scheduleWriteMu.Unlock()
	if err != nil {
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

// MarkLogOutcome updates WON/LOST on an auction log.
//   - Transitioning to WON automatically carves out the won slot(s) from WeeklyAvailability
//     (for recurring NEW/FR classes) or adds a DateAvailabilityOverride BLOCK (for OTR/CU).
//   - Transitioning FROM WON back to LOST/PENDING automatically restores the previously
//     carved weekly window (re-merging adjacent blocks) or removes the matching BLOCK override.
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

	if entry.Parsed != nil && len(entry.MatchedSlots) > 0 {
		refTime := entry.CreatedAt.In(parser.WIBLocation())
		if outcome == domain.OutcomeWon && !wasWon {
			u.applyWonSchedule(accountID, entry.Parsed, entry.MatchedSlots, refTime)
			u.InvalidateAccountCache(accountID)
		} else if wasWon && outcome != domain.OutcomeWon {
			u.restoreWonSchedule(accountID, entry.Parsed, entry.MatchedSlots, refTime)
			u.InvalidateAccountCache(accountID)
		}
	}

	return entry, nil
}

func restoreWeeklyAvailability(accountID uint, weekly []domain.WeeklyAvailability, slots []domain.ScheduleSlot, duration int) []domain.WeeklyAvailability {
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
		byDay[slot.DayOfWeek] = append(byDay[slot.DayOfWeek], TimeRange{
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
