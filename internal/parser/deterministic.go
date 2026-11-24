// internal/parser/deterministic.go
package parser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/azharf99/tele-gateway/internal/domain"
)

// WIBLocation returns Asia/Jakarta (UTC+7), falling back to FixedZone if tzdata is absent.
func WIBLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*3600)
	}
	return loc
}

// ParseOptions configures deterministic parser behavior for a specific tutor profile / target.
type ParseOptions struct {
	DefaultAuctionType domain.AuctionType
	StopKeywords       string // comma-separated; defaults to SOLD,CLOSED,TAKEN,DONE,BATAL,CANCELLED,CANCELED
	PrivateDurationMin int    // default 60
	GroupDurationMin   int    // default 90
}

type courseAlias struct {
	re          *regexp.Regexp
	code        domain.CourseCode
	isGenericDG bool
}

var (
	reURL           = regexp.MustCompile(`https?://[^\s\)\]]+`)
	reTrigger       = regexp.MustCompile(`(?i)\bneed\s+(?:tutor|otr|fr|cu|catch\s*up|ec|extra\s*class|forever\s+replacement|replacement)\b`)
	reCU            = regexp.MustCompile(`(?i)\b(?:catch\s*up|cu|extra\s*class|ec)\b`)
	reFR            = regexp.MustCompile(`(?i)\b(?:forever\s*replacement|fr)\b`)
	reOTR           = regexp.MustCompile(`(?i)\b(?:otr|one\s*time\s*replacement|cuti|tutor\s+sakit|pengganti\s+1x)\b`)
	reNew           = regexp.MustCompile(`(?i)\b(?:new|kelas\s+baru|graduation|after\s+grad(?:uation)?)\b`)
	reGroupFormat   = regexp.MustCompile(`(?i)\b(?:prem|premium|grup|group)\b`)
	rePrivateFormat = regexp.MustCompile(`(?i)\b(?:private|privat|1-1|1\s*on\s*1)\b`)
	reDurationMin   = regexp.MustCompile(`(?i)\bdurasi\s+(\d{2,3})\s*(?:menit|mnt|min)\b`)

	// LMS code patterns:
	// - Private: "IND 1-1 VP to PS 12102 (MON 18.00)" or "IND 1-1 DL 413 (WED 18.30)"
	// - Group:   "IND-RB-401-MON-19.00" (no "1-1")
	reLMSPrivateCode = regexp.MustCompile(`(?i)\bIND[\s-]+1-1[\s-]+([A-Z]{2,5}(?:\s*(?:to|->)\s*[A-Z]{2,5})?)\b`)
	reLMSGroupCode   = regexp.MustCompile(`(?i)\bIND[\s-]+([A-Z]{2,5})[\s-]+\d{2,5}(?:[\s-]+(?:MON|TUE|WED|THU|FRI|SAT|SUN)[\s-]+\d{1,2}[.:]\d{2})?\b`)

	// History / progression clauses to strip before scanning body lines for course
	reHistoryClause = regexp.MustCompile(`(?i)(?:lanjutan\s+dari[^\n]*|after\s+grad(?:uation)?\s+[^\n\)]*|lulusan\s+[^\n\)]*)`)

	// Section splitters (everything at or after Note:/Reason: is secondary for schedule & course)
	reNoteSection = regexp.MustCompile(`(?i)^\s*(?:note|catatan|reason|alasan)\s*:`)

	// Explicit schedule labels
	reScheduleLabel = regexp.MustCompile(`(?i)^\s*(?:jadwal|request|hari|waktu|schedule)\s*[:,\-]`)

	// Timezone equality collapse: "16.00 WIT = 14:00 WIB" -> "14:00 WIB"
	reTZConverted = regexp.MustCompile(`(?i)\b\d{1,2}[.:]\d{2}\s*(?:WIT|WITA)\s*=\s*(\d{1,2}[.:]\d{2})\s*(WIB)\b`)

	// Time tokens: HH:MM or HH.MM (with optional TZ), OR explicit "jam/pukul/pkl HH"
	reTimeToken     = regexp.MustCompile(`(?i)(?:\b(?:pukul|pkl\.?|jam)\s+)?(\d{1,2})[.:](\d{2})(?:\s*(WIB|WITA|WIT))?\b`)
	reHourOnlyToken = regexp.MustCompile(`(?i)\b(?:pukul|pkl\.?|jam)\s+(\d{1,2})(?:\s*(WIB|WITA|WIT))?\b`)

	// Date tokens: "24 Nov 2026", "8 Desember 2026", "28-Nov-2026", "27/11/2026"
	reDateText    = regexp.MustCompile(`(?i)\b(\d{1,2})[\s\-]+(jan(?:uari|uary)?|feb(?:ruari|ruary)?|mar(?:et|ch)?|apr(?:il)?|mei|may|jun(?:i|e)?|jul(?:i|y)?|agu(?:stus)?|aug(?:ust)?|sep(?:t(?:ember)?)?|okt(?:ober)?|oct(?:ober)?|nov(?:ember)?|des(?:ember)?|dec(?:ember)?)[\s\-]+(\d{4})\b`)
	reDateNumeric = regexp.MustCompile(`\b(\d{1,2})/(\d{1,2})/(\d{4})\b`)

	// Day tokens (Indonesian & English + relative)
	reDayWord = regexp.MustCompile(`(?i)\b(senin|monday|mon|selasa|tuesday|tue|tues|rabu|wednesday|wed|kamis|thursday|thu|thur|thurs|jum['’]?at|friday|fri|sabtu|saturday|sat|minggu|ahad|sunday|sun|hari\s+ini|today|besok|tomorrow)\b`)

	// Combined token scanner for schedule parsing
	reScheduleTok = regexp.MustCompile(`(?i)\b(senin|monday|mon|selasa|tuesday|tue|tues|rabu|wednesday|wed|kamis|thursday|thu|thur|thurs|jum['’]?at|friday|fri|sabtu|saturday|sat|minggu|ahad|sunday|sun|hari\s+ini|today|besok|tomorrow)\b|(?:\b(?:pukul|pkl\.?|jam)\s+)?(\d{1,2})[.:](\d{2})(?:\s*(WIB|WITA|WIT))?\b|\b(?:pukul|pkl\.?|jam)\s+(\d{1,2})(?:\s*(WIB|WITA|WIT))?\b|(\s*/\s*|\batau\b|\bor\b)|(,|\bdan\b|&)`)

	// Gender, Language, NO NEGO
	reNoNego      = regexp.MustCompile(`(?i)\bno\s*nego\b`)
	reGenderAny   = regexp.MustCompile(`(?i)(?:perempuan\s*/\s*laki|laki\w*\s*/\s*perempuan|male\s*/\s*female|female\s*/\s*male|cewek\s*/\s*cowok|cowok\s*/\s*cewek|tutor\s*[:\-]?\s*bebas|gender\s*[:\-]?\s*bebas|bebas\s+gender|aman\s+gpp)`)
	reGenderFem   = regexp.MustCompile(`(?i)\b(?:perempuan|wanita|cewek|female|tutor\s+pr)\b`)
	reGenderMale  = regexp.MustCompile(`(?i)\b(?:laki[\s\-]*laki|laki2|pria|cowok|male|tutor\s+lk)\b`)
	reLangFullEng = regexp.MustCompile(`(?i)(?:\bfull\s*eng(?:lish)?\b|\bonly\s+english\b|\benglish\s+only\b|tidak\s+bisa\s+indo)`)
	reLangBiling  = regexp.MustCompile(`(?i)\b(?:bilingual|mix\s*eng(?:lish)?|semi\s*eng(?:lish)?)\b`)
	reLangAny     = regexp.MustCompile(`(?i)\bbahasa\s*[:\-]?\s*bebas\b`)
	reLangIndo    = regexp.MustCompile(`(?i)(?:\bbahasa\s*[:\-]?\s*indo(?:nesia)?\b|\bindo(?:nesia)?\s+only\b)`)

	// Ordered longest/most-specific aliases first so PS2 beats PS, DGS beats DG, etc.
	courseAliases = []courseAlias{
		{regexp.MustCompile(`(?i)\bpython\s+start\s*2\b`), domain.CoursePS2, false},
		{regexp.MustCompile(`(?i)\bpython\s+start\s*1\b`), domain.CoursePS1, false},
		{regexp.MustCompile(`(?i)\bpython\s+start\b`), domain.CoursePS1, false},
		{regexp.MustCompile(`(?i)\bpython\s+pro\s*2\b`), domain.CoursePP2, false},
		{regexp.MustCompile(`(?i)\bpython\s+pro\s*1\b`), domain.CoursePP1, false},
		{regexp.MustCompile(`(?i)\bpython\s+pro\b`), domain.CoursePP1, false},
		{regexp.MustCompile(`(?i)\bfrontend\s+development\b`), domain.CourseFE, false},
		{regexp.MustCompile(`(?i)\bfront[\s\-]*end\b`), domain.CourseFE, false},
		{regexp.MustCompile(`(?i)\b(?:design\s+graphic|graphic\s+design|grap\s*des|grapdes|gd|dg)\s+(?:senior|sr\.?)\b`), domain.CourseDGS, false},
		{regexp.MustCompile(`(?i)\b(?:design\s+graphic|graphic\s+design|grap\s*des|grapdes|gd|dg)\s+(?:junior|jr\.?)\b`), domain.CourseDGJ, false},
		{regexp.MustCompile(`(?i)\bvisual\s+prog(?:ramming)?\b`), domain.CourseVP, false},
		{regexp.MustCompile(`(?i)\bcoding\s+knight\b`), domain.CourseCK, false},
		{regexp.MustCompile(`(?i)\bdigital\s+literacy\b`), domain.CourseDL, false},
		{regexp.MustCompile(`(?i)\bartificial\s+intel[ie]gence\b`), domain.CourseAI, false},
		{regexp.MustCompile(`(?i)\bgame\s+design\b`), domain.CourseRB, false},
		{regexp.MustCompile(`(?i)\broblox\b`), domain.CourseRB, false},
		{regexp.MustCompile(`(?i)\bunity\b`), domain.CourseUnity, false},
		{regexp.MustCompile(`(?i)\bps\s*2\b`), domain.CoursePS2, false},
		{regexp.MustCompile(`(?i)\bps\s*1\b`), domain.CoursePS1, false},
		{regexp.MustCompile(`(?i)\bpp\s*2\b`), domain.CoursePP2, false},
		{regexp.MustCompile(`(?i)\bpp\s*1\b`), domain.CoursePP1, false},
		{regexp.MustCompile(`(?i)\bdgs\b`), domain.CourseDGS, false},
		{regexp.MustCompile(`(?i)\bdgj\b`), domain.CourseDGJ, false},
		{regexp.MustCompile(`(?i)\b(?:design\s+graphic|graphic\s+design|grap\s*des|grapdes)\b`), domain.CourseDGJ, true},
		{regexp.MustCompile(`(?i)\bps\b`), domain.CoursePS1, false},
		{regexp.MustCompile(`(?i)\bpp\b`), domain.CoursePP1, false},
		{regexp.MustCompile(`(?i)\bfe\b`), domain.CourseFE, false},
		{regexp.MustCompile(`(?i)\bdg\b`), domain.CourseDGJ, false},
		{regexp.MustCompile(`(?i)\bvp\b`), domain.CourseVP, false},
		{regexp.MustCompile(`(?i)\bck\b`), domain.CourseCK, false},
		{regexp.MustCompile(`(?i)\bdl\b`), domain.CourseDL, false},
		{regexp.MustCompile(`(?i)\bai\b`), domain.CourseAI, false},
		{regexp.MustCompile(`(?i)\brb\b`), domain.CourseRB, false},
	}

	monthLookup = map[string]time.Month{
		"jan": time.January, "januari": time.January, "january": time.January,
		"feb": time.February, "februari": time.February, "february": time.February,
		"mar": time.March, "maret": time.March, "march": time.March,
		"apr": time.April, "april": time.April,
		"mei": time.May, "may": time.May,
		"jun": time.June, "juni": time.June, "june": time.June,
		"jul": time.July, "juli": time.July, "july": time.July,
		"agu": time.August, "agustus": time.August, "aug": time.August, "august": time.August,
		"sep": time.September, "sept": time.September, "september": time.September,
		"okt": time.October, "oktober": time.October, "oct": time.October, "october": time.October,
		"nov": time.November, "november": time.November,
		"des": time.December, "desember": time.December, "dec": time.December, "december": time.December,
	}
)

// ParseDeterministic parses an Algonova CS auction message purely in-memory (<1ms).
func ParseDeterministic(rawText string, now time.Time, opts ParseOptions) *domain.ParsedAuction {
	if now.IsZero() {
		now = time.Now().In(WIBLocation())
	} else {
		now = now.In(WIBLocation())
	}
	if opts.PrivateDurationMin <= 0 {
		opts.PrivateDurationMin = 60
	}
	if opts.GroupDurationMin <= 0 {
		opts.GroupDurationMin = 90
	}

	res := &domain.ParsedAuction{
		RequiredGender: domain.GenderAny,
		Language:       domain.LanguageAny,
		Schedules:      []domain.ScheduleSlot{},
		Confidence:     domain.ConfidenceLow,
	}

	// 1. Extract LeadURL & sanitize text (strip URLs + invisible chars + decorative emojis)
	if urls := reURL.FindAllString(rawText, -1); len(urls) > 0 {
		res.LeadURL = urls[0]
		for _, u := range urls {
			if strings.Contains(strings.ToLower(u), "amocrm") {
				res.LeadURL = u
				break
			}
		}
	}

	clean := sanitizeText(reURL.ReplaceAllString(rawText, " "))
	if strings.TrimSpace(clean) == "" {
		return res
	}

	// 2. Trigger check
	if !reTrigger.MatchString(clean) {
		return res
	}
	res.IsAuctionPost = true

	// 3. Stop-keyword check (e.g. SOLD, CLOSED, TAKEN)
	if kw := findStopKeyword(clean, opts.StopKeywords); kw != "" {
		res.HasStopKeyword = true
		res.StopKeywordFound = kw
	}

	// Split primary body (above Note:/Reason:) vs full text
	primaryLines, _ := splitLinesAndPrimary(clean)
	primaryText := strings.Join(primaryLines, "\n")

	// 4. Auction type detection (CU -> FR -> OTR -> NEW)
	res.AuctionType = detectAuctionType(primaryText, clean, opts.DefaultAuctionType)

	// 5. Class format & duration (Private 60m vs Group/PREM 90m)
	res.ClassFormat, res.DurationMin = detectFormatAndDuration(primaryText, clean, opts)

	// 6. Course detection (4-pass anti-false-positive)
	course, isGenericDG, courseConf := detectCourse(primaryLines)
	res.Course = course
	res.IsGenericDG = isGenericDG

	// 7. Schedule & timezone extraction (WIB)
	slots, logic, schedConf := detectSchedules(primaryLines, now)
	res.Schedules = slots
	res.ScheduleLogic = logic

	// 8. Gender, Language, NO NEGO (scan full clean text because Note: often holds language/gender)
	res.NoNego = reNoNego.MatchString(clean)
	res.RequiredGender = detectGender(clean)
	res.Language = detectLanguage(clean)

	// 9. Overall confidence
	if res.Course != "" && len(res.Schedules) > 0 {
		if courseConf == domain.ConfidenceHigh && schedConf == domain.ConfidenceHigh {
			res.Confidence = domain.ConfidenceHigh
		} else {
			res.Confidence = domain.ConfidenceMedium
		}
	} else {
		res.Confidence = domain.ConfidenceLow
		if res.Course == "" {
			res.Notes = append(res.Notes, "course tidak terdeteksi")
		}
		if len(res.Schedules) == 0 {
			res.Notes = append(res.Notes, "jadwal tidak terdeteksi")
		}
	}

	return res
}

// sanitizeText removes zero-width/format characters (like \u2060 word joiner) and
// replaces non-ASCII symbol/emoji bullets with spaces while preserving newlines and ASCII punctuation.
func sanitizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteRune('\n')
		case unicode.Is(unicode.Cf, r): // zero-width space, word joiner \u2060, BOM, etc.
			continue
		case r == '—' || r == '–':
			b.WriteRune('-')
		case r == '’' || r == '‘':
			b.WriteRune('\'')
		case r < 128:
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			// Emojis (✨, 📍, 🙏🏻), pictographs, arrows, etc. become a clean separator space
			b.WriteRune(' ')
		}
	}
	return b.String()
}

func splitLinesAndPrimary(clean string) (primary []string, all []string) {
	inNote := false
	for _, raw := range strings.Split(clean, "\n") {
		line := collapseSpaces(raw)
		if line == "" {
			continue
		}
		all = append(all, line)
		if !inNote && reNoteSection.MatchString(line) {
			inNote = true
		}
		if !inNote {
			primary = append(primary, line)
		}
	}
	if len(primary) == 0 {
		primary = all
	}
	return primary, all
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func findStopKeyword(clean, stopCSV string) string {
	if strings.TrimSpace(stopCSV) == "" {
		stopCSV = "SOLD,CLOSED,TAKEN,DONE,BATAL,CANCELLED,CANCELED"
	}
	for _, raw := range strings.Split(stopCSV, ",") {
		kw := strings.TrimSpace(raw)
		if kw == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
		if err != nil {
			continue
		}
		if re.MatchString(clean) {
			return strings.ToUpper(kw)
		}
	}
	return ""
}

func detectAuctionType(primaryText, fullText string, hint domain.AuctionType) domain.AuctionType {
	switch {
	case reCU.MatchString(primaryText):
		return domain.AuctionTypeCU
	case reFR.MatchString(primaryText):
		return domain.AuctionTypeFR
	case reOTR.MatchString(primaryText):
		return domain.AuctionTypeOTR
	case reNew.MatchString(primaryText):
		return domain.AuctionTypeNew
	case reCU.MatchString(fullText):
		return domain.AuctionTypeCU
	case reFR.MatchString(fullText):
		return domain.AuctionTypeFR
	case reOTR.MatchString(fullText):
		return domain.AuctionTypeOTR
	case hint != "":
		return hint
	default:
		return domain.AuctionTypeNew
	}
}

func detectFormatAndDuration(primaryText, fullText string, opts ParseOptions) (domain.ClassFormat, int) {
	format := domain.ClassFormatPrivate
	duration := opts.PrivateDurationMin

	switch {
	case reGroupFormat.MatchString(primaryText):
		format = domain.ClassFormatGroup
		duration = opts.GroupDurationMin
	case rePrivateFormat.MatchString(primaryText):
		format = domain.ClassFormatPrivate
		duration = opts.PrivateDurationMin
	case reLMSGroupCode.MatchString(primaryText) && !reLMSPrivateCode.MatchString(primaryText):
		// Algonova convention: IND-RB-401-... (without "1-1") is a Group (PREM) 90-min class
		format = domain.ClassFormatGroup
		duration = opts.GroupDurationMin
	case reGroupFormat.MatchString(fullText):
		format = domain.ClassFormatGroup
		duration = opts.GroupDurationMin
	}

	if m := reDurationMin.FindStringSubmatch(fullText); len(m) == 2 {
		if mins, err := strconv.Atoi(m[1]); err == nil && mins >= 15 && mins <= 300 {
			duration = mins
		}
	}
	return format, duration
}

func detectCourse(primaryLines []string) (domain.CourseCode, bool, domain.ParseConfidence) {
	// Pass 1: Line containing the trigger ("Need tutor UNITY...", "... NEED TUTOR PRIVATE DG JUNIOR 📍GRAP DES JR")
	for _, line := range primaryLines {
		if !reTrigger.MatchString(line) {
			continue
		}
		// Inspect text starting from the trigger word onward first so preceding LMS prefixes don't override explicit labels
		if idx := reTrigger.FindStringIndex(line); idx != nil {
			afterTrigger := stripHistoryClauses(line[idx[0]:])
			if code, gen, ok := matchCourseInLine(afterTrigger); ok {
				return code, gen, domain.ConfidenceHigh
			}
		}
	}

	// Pass 2: LMS group code ("IND 1-1 VP to PS 12102", "IND-RB-401-MON-19.00", "IND 1-1 DL 413")
	for _, line := range primaryLines {
		if m := reLMSPrivateCode.FindStringSubmatch(line); len(m) == 2 {
			target := extractTransitionTarget(m[1])
			if code, gen, ok := matchCourseInLine(target); ok {
				return code, gen, domain.ConfidenceHigh
			}
		}
		if m := reLMSGroupCode.FindStringSubmatch(line); len(m) == 2 {
			if code, gen, ok := matchCourseInLine(m[1]); ok {
				return code, gen, domain.ConfidenceHigh
			}
		}
	}

	// Pass 3: Scan primary lines top-to-bottom after removing history phrases like "(AFTER GRAD AI)" or "lanjutan dari AI to FE to PP2"
	var found []struct {
		code      domain.CourseCode
		isGeneric bool
	}
	for _, line := range primaryLines {
		if reScheduleLabel.MatchString(line) {
			continue
		}
		cleaned := stripHistoryClauses(line)
		if strings.TrimSpace(cleaned) == "" {
			continue
		}
		if code, gen, ok := matchCourseInLine(cleaned); ok {
			// Deduplicate same course appearing twice
			dup := false
			for _, f := range found {
				if f.code == code {
					dup = true
					break
				}
			}
			if !dup {
				found = append(found, struct {
					code      domain.CourseCode
					isGeneric bool
				}{code, gen})
			}
		}
	}

	if len(found) == 1 {
		return found[0].code, found[0].isGeneric, domain.ConfidenceHigh
	}
	if len(found) > 1 {
		// Multiple conflicting courses at the same priority level -> pick first but lower confidence so optional AI fallback can double-check
		return found[0].code, found[0].isGeneric, domain.ConfidenceLow
	}
	return "", false, domain.ConfidenceLow
}

func stripHistoryClauses(line string) string {
	return collapseSpaces(reHistoryClause.ReplaceAllString(line, " "))
}

// extractTransitionTarget turns "VP to PS" or "AI -> FE" into its current active target ("PS", "FE").
func extractTransitionTarget(s string) string {
	lower := strings.ToLower(s)
	if idx := strings.LastIndex(lower, " to "); idx >= 0 {
		return strings.TrimSpace(s[idx+4:])
	}
	if idx := strings.LastIndex(s, "->"); idx >= 0 {
		return strings.TrimSpace(s[idx+2:])
	}
	return strings.TrimSpace(s)
}

func matchCourseInLine(line string) (domain.CourseCode, bool, bool) {
	for _, alias := range courseAliases {
		if alias.re.MatchString(line) {
			return alias.code, alias.isGenericDG, true
		}
	}
	return "", false, false
}

// detectSchedules extracts WIB schedule slots and whether multiple slots are AND vs OR.
func detectSchedules(primaryLines []string, now time.Time) ([]domain.ScheduleSlot, domain.ScheduleLogic, domain.ParseConfidence) {
	// Classify lines into tiers so explicit "Jadwal:" / "REQUEST:" lines override LMS headers
	var (
		labeledLines  []string
		bodyLines     []string
		lmsLines      []string
		floatingDates []time.Time
	)

	for _, line := range primaryLines {
		isLMS := reLMSPrivateCode.MatchString(line) || reLMSGroupCode.MatchString(line)
		if reScheduleLabel.MatchString(line) {
			labeledLines = append(labeledLines, line)
			continue
		}
		if isLMS {
			lmsLines = append(lmsLines, line)
			continue
		}
		bodyLines = append(bodyLines, line)
		// Collect standalone date lines like "27 NOV 2026" or "28 NOV 2026" (without a time on that line)
		if !hasClockTime(line) {
			if dt, ok := parseDateInLine(line, now.Location()); ok {
				floatingDates = append(floatingDates, dt)
			}
		}
	}

	// Tier 1: Explicit labeled lines ("Hari : Senin 19:00", "REQUEST : SENIN / RABU ...", "Jadwal, Tgl 24 Nov 2026, jam 19.00")
	if slots, logic := extractSlotsFromLines(labeledLines, now); len(slots) > 0 {
		attachFloatingDate(slots, floatingDates)
		return slots, logic, domain.ConfidenceHigh
	}

	// Tier 2: Normal body lines (e.g. "(FRI 20.30, SAT 12.00)", "Selasa pukul 19:30 WIB, Start 8 Desember 2026", "Hari ini, 19.00 WIB", "19.00 WIB hari ini")
	if slots, logic := extractSlotsFromLines(bodyLines, now); len(slots) > 0 {
		attachFloatingDate(slots, floatingDates)
		return slots, logic, domain.ConfidenceHigh
	}

	// Tier 3: LMS code lines ("IND 1-1 VP to PS 12102 (MON 18.00)", "IND 1-1 DL 413 (WED 18.30)")
	if slots, logic := extractSlotsFromLines(lmsLines, now); len(slots) > 0 {
		attachFloatingDate(slots, floatingDates)
		return slots, logic, domain.ConfidenceHigh
	}

	return []domain.ScheduleSlot{}, "", domain.ConfidenceLow
}

func hasClockTime(line string) bool {
	return reTimeToken.MatchString(line) || reHourOnlyToken.MatchString(line)
}

func attachFloatingDate(slots []domain.ScheduleSlot, floatingDates []time.Time) {
	if len(floatingDates) == 0 || len(slots) == 0 {
		return
	}
	dt := floatingDates[0]
	for i := range slots {
		if slots[i].SpecificDate == "" && int(dt.Weekday()) == slots[i].DayOfWeek {
			slots[i].SpecificDate = dt.Format("2006-01-02")
		}
	}
}

func extractSlotsFromLines(lines []string, now time.Time) ([]domain.ScheduleSlot, domain.ScheduleLogic) {
	var allSlots []domain.ScheduleSlot
	seen := make(map[string]struct{})
	hasOR := false

	for _, line := range lines {
		lineSlots, lineHasOR := parseScheduleLine(line, now)
		if len(lineSlots) == 0 {
			continue
		}
		if lineHasOR {
			hasOR = true
		}
		for _, s := range lineSlots {
			key := fmt.Sprintf("%d:%d:%s", s.DayOfWeek, s.StartMinute, s.SpecificDate)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			allSlots = append(allSlots, s)
		}
	}

	if len(allSlots) == 0 {
		return nil, ""
	}
	if len(allSlots) == 1 {
		return allSlots, domain.ScheduleLogicSingle
	}
	if hasOR {
		return allSlots, domain.ScheduleLogicOR
	}
	return allSlots, domain.ScheduleLogicAND
}

type dayRef struct {
	weekday      int
	specificDate string
	raw          string
}

func parseScheduleLine(line string, now time.Time) ([]domain.ScheduleSlot, bool) {
	// Collapse explicit timezone conversions first: "16.00 WIT = 14:00 WIB" -> "14:00 WIB"
	norm := reTZConverted.ReplaceAllString(line, "$1 $2")

	// Extract an explicit date on this line (if any), e.g. "Start 8 Desember 2026" or "Tgl 24 Nov 2026"
	lineDate, hasLineDate := parseDateInLine(norm, now.Location())

	// Remove the date substring before scanning clock times so numbers in dates aren't confused
	scanStr := reDateText.ReplaceAllString(norm, " ")
	scanStr = reDateNumeric.ReplaceAllString(scanStr, " ")

	matches := reScheduleTok.FindAllStringSubmatch(scanStr, -1)

	var (
		slots       []domain.ScheduleSlot
		pendingDays []dayRef
		pendingTime *int // handles "<time> <day>" like "19.00 WIB hari ini"
		hasOR       bool
	)

	for _, m := range matches {
		switch {
		case m[1] != "": // Day word
			day, ok := resolveDayWord(m[1], now)
			if !ok {
				continue
			}
			if pendingTime != nil && len(pendingDays) == 0 {
				// Time preceded day: "19.00 WIB hari ini"
				slot := domain.ScheduleSlot{
					DayOfWeek:    day.weekday,
					StartMinute:  *pendingTime,
					SpecificDate: day.specificDate,
					RawText:      fmt.Sprintf("%s %02d:%02d", day.raw, *pendingTime/60, *pendingTime%60),
				}
				if slot.SpecificDate == "" && hasLineDate && int(lineDate.Weekday()) == slot.DayOfWeek {
					slot.SpecificDate = lineDate.Format("2006-01-02")
				}
				slots = append(slots, slot)
				pendingTime = nil
			} else {
				pendingDays = append(pendingDays, day)
			}

		case m[2] != "" && m[3] != "": // HH:MM or HH.MM (+ optional TZ)
			mins, ok := parseMinutesWIB(m[2], m[3], m[4])
			if !ok {
				continue
			}
			if len(pendingDays) > 0 {
				for _, d := range pendingDays {
					slot := domain.ScheduleSlot{
						DayOfWeek:    d.weekday,
						StartMinute:  mins,
						SpecificDate: d.specificDate,
						RawText:      fmt.Sprintf("%s %02d:%02d", d.raw, mins/60, mins%60),
					}
					if slot.SpecificDate == "" && hasLineDate && int(lineDate.Weekday()) == slot.DayOfWeek {
						slot.SpecificDate = lineDate.Format("2006-01-02")
					}
					slots = append(slots, slot)
				}
				pendingDays = nil
				pendingTime = nil
			} else {
				tVal := mins
				pendingTime = &tVal
			}

		case m[5] != "": // "jam 19" / "pukul 10"
			mins, ok := parseMinutesWIB(m[5], "00", m[6])
			if !ok {
				continue
			}
			if len(pendingDays) > 0 {
				for _, d := range pendingDays {
					slot := domain.ScheduleSlot{
						DayOfWeek:    d.weekday,
						StartMinute:  mins,
						SpecificDate: d.specificDate,
						RawText:      fmt.Sprintf("%s %02d:%02d", d.raw, mins/60, mins%60),
					}
					if slot.SpecificDate == "" && hasLineDate && int(lineDate.Weekday()) == slot.DayOfWeek {
						slot.SpecificDate = lineDate.Format("2006-01-02")
					}
					slots = append(slots, slot)
				}
				pendingDays = nil
				pendingTime = nil
			} else {
				tVal := mins
				pendingTime = &tVal
			}

		case m[7] != "": // "/" or "atau" or "or"
			if len(pendingDays) > 0 || len(slots) > 0 {
				hasOR = true
			}
		}
	}

	// Date without weekday name + time (e.g. "Jadwal, Tgl 24 Nov 2026, jam 19.00")
	if len(slots) == 0 && hasLineDate && pendingTime != nil {
		slots = append(slots, domain.ScheduleSlot{
			DayOfWeek:    int(lineDate.Weekday()),
			StartMinute:  *pendingTime,
			SpecificDate: lineDate.Format("2006-01-02"),
			RawText:      fmt.Sprintf("%s %02d:%02d", lineDate.Format("2006-01-02"), *pendingTime/60, *pendingTime%60),
		})
	}

	return slots, hasOR
}

func parseMinutesWIB(hourStr, minStr, tz string) (int, bool) {
	h, err1 := strconv.Atoi(hourStr)
	m, err2 := strconv.Atoi(minStr)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	total := h*60 + m
	switch strings.ToUpper(strings.TrimSpace(tz)) {
	case "WITA":
		total -= 60
	case "WIT":
		total -= 120
	}
	if total < 0 {
		total += 1440
	}
	return total, true
}

func resolveDayWord(raw string, now time.Time) (dayRef, bool) {
	w := strings.ToLower(collapseSpaces(raw))
	w = strings.ReplaceAll(w, "'", "")
	switch w {
	case "minggu", "ahad", "sunday", "sun":
		return dayRef{weekday: int(time.Sunday), raw: raw}, true
	case "senin", "monday", "mon":
		return dayRef{weekday: int(time.Monday), raw: raw}, true
	case "selasa", "tuesday", "tue", "tues":
		return dayRef{weekday: int(time.Tuesday), raw: raw}, true
	case "rabu", "wednesday", "wed":
		return dayRef{weekday: int(time.Wednesday), raw: raw}, true
	case "kamis", "thursday", "thu", "thur", "thurs":
		return dayRef{weekday: int(time.Thursday), raw: raw}, true
	case "jumat", "friday", "fri":
		return dayRef{weekday: int(time.Friday), raw: raw}, true
	case "sabtu", "saturday", "sat":
		return dayRef{weekday: int(time.Saturday), raw: raw}, true
	case "hari ini", "today":
		return dayRef{weekday: int(now.Weekday()), specificDate: now.Format("2006-01-02"), raw: raw}, true
	case "besok", "tomorrow":
		tmr := now.AddDate(0, 0, 1)
		return dayRef{weekday: int(tmr.Weekday()), specificDate: tmr.Format("2006-01-02"), raw: raw}, true
	default:
		return dayRef{}, false
	}
}

func parseDateInLine(line string, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = WIBLocation()
	}
	if m := reDateText.FindStringSubmatch(line); len(m) == 4 {
		day, err1 := strconv.Atoi(m[1])
		month, ok := monthLookup[strings.ToLower(m[2])]
		year, err2 := strconv.Atoi(m[3])
		if err1 == nil && ok && err2 == nil && day >= 1 && day <= 31 && year >= 2020 && year <= 2100 {
			dt := time.Date(year, month, day, 0, 0, 0, 0, loc)
			if dt.Day() == day && dt.Month() == month {
				return dt, true
			}
		}
	}
	if m := reDateNumeric.FindStringSubmatch(line); len(m) == 4 {
		day, err1 := strconv.Atoi(m[1])
		monthInt, err2 := strconv.Atoi(m[2])
		year, err3 := strconv.Atoi(m[3])
		if err1 == nil && err2 == nil && err3 == nil && day >= 1 && day <= 31 && monthInt >= 1 && monthInt <= 12 && year >= 2020 && year <= 2100 {
			dt := time.Date(year, time.Month(monthInt), day, 0, 0, 0, 0, loc)
			if dt.Day() == day && int(dt.Month()) == monthInt {
				return dt, true
			}
		}
	}
	return time.Time{}, false
}

func detectGender(clean string) domain.Gender {
	if reGenderAny.MatchString(clean) {
		return domain.GenderAny
	}
	hasFem := reGenderFem.MatchString(clean)
	hasMale := reGenderMale.MatchString(clean)
	if hasFem && hasMale {
		return domain.GenderAny
	}
	if hasFem {
		return domain.GenderFemale
	}
	if hasMale {
		return domain.GenderMale
	}
	return domain.GenderAny
}

func detectLanguage(clean string) domain.Language {
	switch {
	case reLangFullEng.MatchString(clean):
		return domain.LanguageFullEnglish
	case reLangBiling.MatchString(clean):
		return domain.LanguageBilingual
	case reLangAny.MatchString(clean):
		return domain.LanguageAny
	case reLangIndo.MatchString(clean):
		return domain.LanguageIndonesia
	default:
		return domain.LanguageAny
	}
}
