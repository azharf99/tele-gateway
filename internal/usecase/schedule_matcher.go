// internal/usecase/schedule_matcher.go
package usecase

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/azharf99/tele-gateway/internal/domain"
)

// TimeRange is a half-open minute interval [Start, End) within a single day (0..1440).
type TimeRange struct {
	Start int
	End   int
}

// MergeTimeRanges sorts and merges overlapping or directly adjacent time windows
// (e.g. [19:00,20:00) + [20:00,21:00) -> [19:00,21:00) so a 90-minute class fits).
func MergeTimeRanges(ranges []TimeRange) []TimeRange {
	valid := make([]TimeRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Start < 0 {
			r.Start = 0
		}
		if r.End > 1440 {
			r.End = 1440
		}
		if r.End > r.Start {
			valid = append(valid, r)
		}
	}
	if len(valid) <= 1 {
		return valid
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].Start != valid[j].Start {
			return valid[i].Start < valid[j].Start
		}
		return valid[i].End < valid[j].End
	})

	merged := []TimeRange{valid[0]}
	for _, cur := range valid[1:] {
		last := &merged[len(merged)-1]
		if cur.Start <= last.End {
			if cur.End > last.End {
				last.End = cur.End
			}
		} else {
			merged = append(merged, cur)
		}
	}
	return merged
}

// SubtractTimeRange removes [cut.Start, cut.End) from merged ranges, splitting a
// window into two when cut falls strictly in the middle.
func SubtractTimeRange(ranges []TimeRange, cut TimeRange) []TimeRange {
	if cut.End <= cut.Start {
		return MergeTimeRanges(ranges)
	}
	base := MergeTimeRanges(ranges)
	out := make([]TimeRange, 0, len(base)+1)
	for _, r := range base {
		if cut.End <= r.Start || cut.Start >= r.End {
			out = append(out, r)
			continue
		}
		if cut.Start > r.Start {
			out = append(out, TimeRange{Start: r.Start, End: cut.Start})
		}
		if cut.End < r.End {
			out = append(out, TimeRange{Start: cut.End, End: r.End})
		}
	}
	return out
}

// EffectiveAvailability returns the merged free time windows for a given weekday,
// applying EXTRA and BLOCK date overrides when specificDate (YYYY-MM-DD) is set
// and applyDateOverrides is true (for 1-time classes like OTR / CU).
func EffectiveAvailability(
	weekly []domain.WeeklyAvailability,
	overrides []domain.DateAvailabilityOverride,
	dayOfWeek int,
	specificDate string,
	applyDateOverrides bool,
) []TimeRange {
	var ranges []TimeRange
	for _, w := range weekly {
		if w.IsActive && w.DayOfWeek == dayOfWeek {
			ranges = append(ranges, TimeRange{Start: w.StartMinute, End: w.EndMinute})
		}
	}

	if applyDateOverrides && specificDate != "" {
		// First add EXTRA windows, merge, then subtract BLOCK windows.
		for _, ov := range overrides {
			if ov.Date == specificDate && ov.Kind == domain.OverrideExtra {
				ranges = append(ranges, TimeRange{Start: ov.StartMinute, End: ov.EndMinute})
			}
		}
		ranges = MergeTimeRanges(ranges)
		for _, ov := range overrides {
			if ov.Date == specificDate && ov.Kind == domain.OverrideBlock {
				ranges = SubtractTimeRange(ranges, TimeRange{Start: ov.StartMinute, End: ov.EndMinute})
			}
		}
		return ranges
	}

	return MergeTimeRanges(ranges)
}

func FitsInRanges(ranges []TimeRange, startMin, durationMin int) bool {
	if durationMin <= 0 {
		durationMin = 60
	}
	endMin := startMin + durationMin
	if startMin < 0 || endMin > 1440 {
		return false
	}
	for _, r := range ranges {
		if startMin >= r.Start && endMin <= r.End {
			return true
		}
	}
	return false
}

type EvaluationResult struct {
	Matched         bool
	Decision        domain.AuctionDecision
	SkipReason      string
	ReplyText       string
	NeedsGenderNego bool
	MatchedSlots    []domain.ScheduleSlot
}

var indonesianDays = [7]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}

func FormatSlotIndonesian(slot domain.ScheduleSlot) string {
	day := "?"
	if slot.DayOfWeek >= 0 && slot.DayOfWeek < 7 {
		day = indonesianDays[slot.DayOfWeek]
	}
	return fmt.Sprintf("%s %02d:%02d", day, slot.StartMinute/60, slot.StartMinute%60)
}

// EvaluateAuction checks whether a parsed CS auction message matches the tutor's
// profile, capabilities, gender/language rules, and schedule availability.
func EvaluateAuction(
	profile *domain.ClassAuctionProfile,
	parsed *domain.ParsedAuction,
	weekly []domain.WeeklyAvailability,
	overrides []domain.DateAvailabilityOverride,
	isCoolingDown func(slot domain.ScheduleSlot) bool,
) EvaluationResult {
	if parsed == nil || !parsed.IsAuctionPost {
		return EvaluationResult{Decision: domain.DecisionIgnored, SkipReason: "bukan pesan lelang kelas"}
	}
	if parsed.HasStopKeyword {
		return EvaluationResult{
			Decision:   domain.DecisionIgnored,
			SkipReason: fmt.Sprintf("mengandung stop keyword (%s)", parsed.StopKeywordFound),
		}
	}
	if profile == nil || !profile.IsActive {
		return EvaluationResult{Decision: domain.DecisionSkipped, SkipReason: "profil lelang kelas sedang nonaktif"}
	}

	// 1. Auction type filter (NEW, OTR, CU, FR)
	if !containsFold(profile.AllowedAuctionTypes, string(parsed.AuctionType)) {
		return EvaluationResult{
			Decision:   domain.DecisionSkipped,
			SkipReason: fmt.Sprintf("jenis lelang %s tidak diaktifkan", parsed.AuctionType),
		}
	}

	// 2. Format filter (PRIVATE 60m vs GROUP 90m)
	if !containsFold(profile.AllowedFormats, string(parsed.ClassFormat)) {
		return EvaluationResult{
			Decision:   domain.DecisionSkipped,
			SkipReason: fmt.Sprintf("format kelas %s tidak diaktifkan", parsed.ClassFormat),
		}
	}

	// 3. Course filter
	if parsed.Course == "" {
		return EvaluationResult{
			Decision:   domain.DecisionSkipped,
			SkipReason: "mata pelajaran tidak terdeteksi",
		}
	}
	if !matchesAllowedCourse(profile, parsed.Course, parsed.IsGenericDG) {
		return EvaluationResult{
			Decision:   domain.DecisionSkipped,
			SkipReason: fmt.Sprintf("mata pelajaran %s tidak ada di daftar keahlian tutor", parsed.Course),
		}
	}

	// 4. Language filter
	if parsed.Language != "" && parsed.Language != domain.LanguageAny {
		if !containsFold(profile.AllowedLanguages, string(parsed.Language)) {
			return EvaluationResult{
				Decision:   domain.DecisionSkipped,
				SkipReason: fmt.Sprintf("bahasa %s tidak sesuai profil tutor", parsed.Language),
			}
		}
	}

	// 5. Gender filter + Gender Negotiation + NO NEGO
	needsNego := false
	if parsed.RequiredGender != "" && parsed.RequiredGender != domain.GenderAny && parsed.RequiredGender != profile.TutorGender {
		if !profile.AllowGenderNego {
			return EvaluationResult{
				Decision:   domain.DecisionSkipped,
				SkipReason: fmt.Sprintf("khusus tutor %s (nego gender nonaktif)", parsed.RequiredGender),
			}
		}
		if profile.RespectNoNego && parsed.NoNego {
			return EvaluationResult{
				Decision:   domain.DecisionSkipped,
				SkipReason: fmt.Sprintf("khusus tutor %s dan tertera NO NEGO", parsed.RequiredGender),
			}
		}
		needsNego = true
	}

	// 6. Schedule availability check (weekly + date overrides for 1-time OTR/CU)
	if len(parsed.Schedules) == 0 {
		return EvaluationResult{
			Decision:   domain.DecisionSkipped,
			SkipReason: "jadwal tidak terdeteksi dari pesan",
		}
	}

	duration := parsed.DurationMin
	if duration <= 0 {
		if parsed.ClassFormat == domain.ClassFormatGroup {
			duration = profile.GroupDurationMin
			if duration <= 0 {
				duration = 90
			}
		} else {
			duration = profile.PrivateDurationMin
			if duration <= 0 {
				duration = 60
			}
		}
	}

	// Date overrides apply to 1-time classes (OTR, CU) or when a dated slot has an explicit BLOCK/EXTRA on that date
	isOneTime := parsed.AuctionType == domain.AuctionTypeOTR || parsed.AuctionType == domain.AuctionTypeCU

	checkSlot := func(slot domain.ScheduleSlot) (bool, string) {
		if isCoolingDown != nil && isCoolingDown(slot) {
			return false, fmt.Sprintf("slot %s sedang dalam cooldown", FormatSlotIndonesian(slot))
		}
		avail := EffectiveAvailability(weekly, overrides, slot.DayOfWeek, slot.SpecificDate, isOneTime)
		if !FitsInRanges(avail, slot.StartMinute, duration) {
			return false, fmt.Sprintf("jadwal %s (%dm) tidak cocok dengan jam kosong", FormatSlotIndonesian(slot), duration)
		}
		return true, ""
	}

	var matchedSlots []domain.ScheduleSlot
	if parsed.ScheduleLogic == domain.ScheduleLogicOR {
		for _, slot := range parsed.Schedules {
			if ok, _ := checkSlot(slot); ok {
				matchedSlots = append(matchedSlots, slot)
			}
		}
		if len(matchedSlots) == 0 {
			labels := make([]string, 0, len(parsed.Schedules))
			for _, s := range parsed.Schedules {
				labels = append(labels, FormatSlotIndonesian(s))
			}
			return EvaluationResult{
				Decision:   domain.DecisionSkipped,
				SkipReason: fmt.Sprintf("tidak ada pilihan jadwal OR (%s, %dm) yang cocok", strings.Join(labels, " / "), duration),
			}
		}
	} else {
		// SINGLE or AND: every slot must fit
		for _, slot := range parsed.Schedules {
			ok, reason := checkSlot(slot)
			if !ok {
				return EvaluationResult{
					Decision:   domain.DecisionSkipped,
					SkipReason: reason,
				}
			}
			matchedSlots = append(matchedSlots, slot)
		}
	}

	// 7. Build reply text
	reply := buildReplyMessage(profile, parsed, needsNego, matchedSlots)
	decision := domain.DecisionBidSent
	if profile.DryRun {
		decision = domain.DecisionDryRunMatch
	}

	// For OR, store the primary picked slot first so "Tandai Menang" knows which slot was bid
	if parsed.ScheduleLogic == domain.ScheduleLogicOR && len(matchedSlots) > 1 {
		matchedSlots = matchedSlots[:1]
	}

	return EvaluationResult{
		Matched:         true,
		Decision:        decision,
		ReplyText:       reply,
		NeedsGenderNego: needsNego,
		MatchedSlots:    matchedSlots,
	}
}

func matchesAllowedCourse(profile *domain.ClassAuctionProfile, course domain.CourseCode, isGenericDG bool) bool {
	if isGenericDG && profile.GenericDGMatchesAny {
		return containsFold(profile.AllowedCourses, string(domain.CourseDGJ)) ||
			containsFold(profile.AllowedCourses, string(domain.CourseDGS))
	}
	return containsFold(profile.AllowedCourses, string(course))
}

func buildReplyMessage(profile *domain.ClassAuctionProfile, parsed *domain.ParsedAuction, needsNego bool, matchedSlots []domain.ScheduleSlot) string {
	var base string
	if needsNego {
		base = strings.TrimSpace(profile.GenderNegoReply)
		if base == "" {
			base = "Me, nego gender"
		}
	} else {
		if profile.ReplyOverrides != nil {
			if override := strings.TrimSpace(profile.ReplyOverrides[string(parsed.AuctionType)]); override != "" {
				base = override
			}
		}
		if base == "" {
			base = strings.TrimSpace(profile.DefaultReply)
		}
		if base == "" {
			base = "Me"
		}
	}

	if parsed.ScheduleLogic == domain.ScheduleLogicOR && profile.IncludeMatchedSlotOnOR && len(matchedSlots) > 0 {
		return fmt.Sprintf("%s, %s", base, FormatSlotIndonesian(matchedSlots[0]))
	}
	return base
}

func containsFold(list []string, target string) bool {
	return slices.ContainsFunc(list, func(item string) bool {
		return strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(target))
	})
}
