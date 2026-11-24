// internal/domain/class_auction.go
package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gotd/td/tg"
	"gorm.io/gorm"
)

var (
	ErrInvalidAuctionConfig = errors.New("invalid class auction configuration")
	ErrAuctionLogNotFound   = errors.New("auction log not found")
	ErrTargetAlreadyExists  = errors.New("watched target already exists")
)

type CourseCode string

const (
	CourseUnity CourseCode = "UNITY"
	CoursePS1   CourseCode = "PS1"
	CoursePS2   CourseCode = "PS2"
	CoursePP1   CourseCode = "PP1"
	CoursePP2   CourseCode = "PP2"
	CourseFE    CourseCode = "FE"
	CourseDGS   CourseCode = "DGS"
	CourseDGJ   CourseCode = "DGJ"
	CourseVP    CourseCode = "VP"
	CourseCK    CourseCode = "CK"
	CourseDL    CourseCode = "DL"
	CourseAI    CourseCode = "AI"
	CourseRB    CourseCode = "RB"
)

type CourseMetadata struct {
	Code    CourseCode `json:"code"`
	Name    string     `json:"name"`
	Aliases []string   `json:"aliases"`
}

func AllCourses() []CourseMetadata {
	return []CourseMetadata{
		{Code: CourseUnity, Name: "Unity", Aliases: []string{"Unity"}},
		{Code: CoursePS1, Name: "Python Start 1", Aliases: []string{"PS1", "PS 1", "PS", "Python Start 1", "Python Start"}},
		{Code: CoursePS2, Name: "Python Start 2", Aliases: []string{"PS2", "PS 2", "Python Start 2"}},
		{Code: CoursePP1, Name: "Python Pro 1", Aliases: []string{"PP1", "PP 1", "PP", "Python Pro 1", "Python Pro"}},
		{Code: CoursePP2, Name: "Python Pro 2", Aliases: []string{"PP2", "PP 2", "Python Pro 2"}},
		{Code: CourseFE, Name: "Frontend Development", Aliases: []string{"FE", "Frontend", "Frontend Development"}},
		{Code: CourseDGS, Name: "Design Graphic Senior", Aliases: []string{"DGS", "DG Senior", "Graphic Design Senior", "Grap Des Sr"}},
		{Code: CourseDGJ, Name: "Design Graphic Junior", Aliases: []string{"DG", "DGJ", "DG Junior", "Graphic Design Junior", "Grap Des Jr"}},
		{Code: CourseVP, Name: "Visual Programming", Aliases: []string{"VP", "Visual Programming"}},
		{Code: CourseCK, Name: "Coding Knight", Aliases: []string{"CK", "Coding Knight"}},
		{Code: CourseDL, Name: "Digital Literacy", Aliases: []string{"DL", "Digital Literacy"}},
		{Code: CourseAI, Name: "Artificial Intelligence", Aliases: []string{"AI", "Artificial Intelligence", "Artificial Intelegence"}},
		{Code: CourseRB, Name: "Roblox", Aliases: []string{"RB", "Roblox", "Game Design"}},
	}
}

func IsValidCourseCode(code CourseCode) bool {
	for _, c := range AllCourses() {
		if c.Code == code {
			return true
		}
	}
	return false
}

type AuctionType string

const (
	AuctionTypeNew AuctionType = "NEW"
	AuctionTypeOTR AuctionType = "OTR"
	AuctionTypeCU  AuctionType = "CU"
	AuctionTypeFR  AuctionType = "FR"
)

type ClassFormat string

const (
	ClassFormatPrivate ClassFormat = "PRIVATE"
	ClassFormatGroup   ClassFormat = "GROUP"
)

type Gender string

const (
	GenderAny    Gender = "ANY"
	GenderMale   Gender = "MALE"
	GenderFemale Gender = "FEMALE"
)

type Language string

const (
	LanguageAny         Language = "ANY"
	LanguageIndonesia   Language = "INDONESIA"
	LanguageBilingual   Language = "BILINGUAL"
	LanguageFullEnglish Language = "FULL_ENGLISH"
)

type ScheduleLogic string

const (
	ScheduleLogicSingle ScheduleLogic = "SINGLE"
	ScheduleLogicAND    ScheduleLogic = "AND"
	ScheduleLogicOR     ScheduleLogic = "OR"
)

type ParseConfidence string

const (
	ConfidenceHigh   ParseConfidence = "HIGH"
	ConfidenceMedium ParseConfidence = "MEDIUM"
	ConfidenceLow    ParseConfidence = "LOW"
)

type AuctionDecision string

const (
	DecisionBidSent     AuctionDecision = "BID_SENT"
	DecisionDryRunMatch AuctionDecision = "DRY_RUN_MATCH"
	DecisionSkipped     AuctionDecision = "SKIPPED"
	DecisionIgnored     AuctionDecision = "IGNORED"
	DecisionError       AuctionDecision = "ERROR"
)

type AuctionOutcome string

const (
	OutcomePending AuctionOutcome = "PENDING"
	OutcomeWon     AuctionOutcome = "WON"
	OutcomeLost    AuctionOutcome = "LOST"
)

type DateOverrideKind string

const (
	OverrideBlock DateOverrideKind = "BLOCK"
	OverrideExtra DateOverrideKind = "EXTRA"
)

// ScheduleSlot represents a single class session time normalized to WIB (Asia/Jakarta).
type ScheduleSlot struct {
	DayOfWeek    int    `json:"day_of_week"`              // 0=Sunday .. 6=Saturday
	StartMinute  int    `json:"start_minute"`             // 0..1439 (e.g. 20:30 -> 1230)
	SpecificDate string `json:"specific_date,omitempty"`  // YYYY-MM-DD when a concrete date is present
	RawText      string `json:"raw_text,omitempty"`
}

// ParsedAuction holds the structured extraction result of an Algonova CS message.
type ParsedAuction struct {
	IsAuctionPost    bool            `json:"is_auction_post"`
	HasStopKeyword   bool            `json:"has_stop_keyword"`
	StopKeywordFound string          `json:"stop_keyword_found,omitempty"`
	Course           CourseCode      `json:"course,omitempty"`
	IsGenericDG      bool            `json:"is_generic_dg,omitempty"`
	AuctionType      AuctionType     `json:"auction_type,omitempty"`
	ClassFormat      ClassFormat     `json:"class_format,omitempty"`
	DurationMin      int             `json:"duration_min"`
	Schedules        []ScheduleSlot  `json:"schedules"`
	ScheduleLogic    ScheduleLogic   `json:"schedule_logic,omitempty"`
	RequiredGender   Gender          `json:"required_gender"`
	Language         Language        `json:"language"`
	NoNego           bool            `json:"no_nego"`
	LeadURL          string          `json:"lead_url,omitempty"`
	Confidence       ParseConfidence `json:"confidence"`
	Notes            []string        `json:"notes,omitempty"`
}

// ClassAuctionProfile stores tutor capabilities, filters, reply templates, and safety toggles per Telegram account.
type ClassAuctionProfile struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	AccountID uint `gorm:"uniqueIndex;not null" json:"account_id"`

	IsActive bool `gorm:"default:true" json:"is_active"`
	DryRun   bool `gorm:"default:true" json:"dry_run"` // Default safe Shadow Mode until user enables live bidding

	// Tutor identity & capabilities (JSON arrays in DB for clean Postgres compatibility)
	TutorGender             Gender   `gorm:"type:varchar(16);default:'MALE'" json:"tutor_gender"`
	AllowedCoursesJSON      string   `gorm:"type:text;default:'[]'" json:"-"`
	AllowedCourses          []string `gorm:"-" json:"allowed_courses"`
	GenericDGMatchesAny     bool     `gorm:"default:false" json:"generic_dg_matches_any"`
	AllowedLanguagesJSON    string   `gorm:"type:text;default:'[\"INDONESIA\",\"BILINGUAL\",\"FULL_ENGLISH\"]'" json:"-"`
	AllowedLanguages        []string `gorm:"-" json:"allowed_languages"`
	AllowedAuctionTypesJSON string   `gorm:"type:text;default:'[\"NEW\",\"OTR\",\"CU\",\"FR\"]'" json:"-"`
	AllowedAuctionTypes     []string `gorm:"-" json:"allowed_auction_types"`
	AllowedFormatsJSON      string   `gorm:"type:text;default:'[\"PRIVATE\",\"GROUP\"]'" json:"-"`
	AllowedFormats          []string `gorm:"-" json:"allowed_formats"`
	PrivateDurationMin      int      `gorm:"default:60" json:"private_duration_min"`
	GroupDurationMin        int      `gorm:"default:90" json:"group_duration_min"`

	// Gender negotiation
	AllowGenderNego bool `gorm:"default:true" json:"allow_gender_nego"`
	RespectNoNego   bool `gorm:"default:true" json:"respect_no_nego"`

	// Reply templates & speed
	DefaultReply           string            `gorm:"type:varchar(255);default:'Me'" json:"default_reply"`
	GenderNegoReply        string            `gorm:"type:varchar(255);default:'Me, nego gender'" json:"gender_nego_reply"`
	ReplyOverridesJSON     string            `gorm:"type:text;default:'{}'" json:"-"`
	ReplyOverrides         map[string]string `gorm:"-" json:"reply_overrides"`
	IncludeMatchedSlotOnOR bool              `gorm:"default:true" json:"include_matched_slot_on_or"`
	MinDelayMs             int               `gorm:"default:0" json:"min_delay_ms"`
	MaxDelayMs             int               `gorm:"default:0" json:"max_delay_ms"`
	SlotCooldownSec        int               `gorm:"default:0" json:"slot_cooldown_sec"`

	// Parsing & safety
	EnableAIFallback    bool   `gorm:"default:false" json:"enable_ai_fallback"`
	IgnoreReplies       bool   `gorm:"default:true" json:"ignore_replies"`
	StopKeywords        string `gorm:"type:text;default:'SOLD,CLOSED,TAKEN,DONE,BATAL,CANCELLED,CANCELED'" json:"stop_keywords"`
	NotifySavedMessages bool   `gorm:"default:true" json:"notify_saved_messages"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func DefaultClassAuctionProfile(accountID uint) *ClassAuctionProfile {
	p := &ClassAuctionProfile{
		AccountID:              accountID,
		IsActive:               true,
		DryRun:                 true,
		TutorGender:            GenderMale,
		AllowedCourses:         []string{},
		GenericDGMatchesAny:    false,
		AllowedLanguages:       []string{string(LanguageIndonesia), string(LanguageBilingual), string(LanguageFullEnglish)},
		AllowedAuctionTypes:    []string{string(AuctionTypeNew), string(AuctionTypeOTR), string(AuctionTypeCU), string(AuctionTypeFR)},
		AllowedFormats:         []string{string(ClassFormatPrivate), string(ClassFormatGroup)},
		PrivateDurationMin:     60,
		GroupDurationMin:       90,
		AllowGenderNego:        true,
		RespectNoNego:          true,
		DefaultReply:           "Me",
		GenderNegoReply:        "Me, nego gender",
		ReplyOverrides:         map[string]string{},
		IncludeMatchedSlotOnOR: true,
		MinDelayMs:             0,
		MaxDelayMs:             0,
		SlotCooldownSec:        0,
		EnableAIFallback:       false,
		IgnoreReplies:          true,
		StopKeywords:           "SOLD,CLOSED,TAKEN,DONE,BATAL,CANCELLED,CANCELED",
		NotifySavedMessages:    true,
	}
	p.MarshalJSONFields()
	return p
}

func (p *ClassAuctionProfile) MarshalJSONFields() {
	if p.AllowedCourses == nil {
		p.AllowedCourses = []string{}
	}
	if p.AllowedLanguages == nil {
		p.AllowedLanguages = []string{}
	}
	if p.AllowedAuctionTypes == nil {
		p.AllowedAuctionTypes = []string{}
	}
	if p.AllowedFormats == nil {
		p.AllowedFormats = []string{}
	}
	if p.ReplyOverrides == nil {
		p.ReplyOverrides = map[string]string{}
	}
	if b, err := json.Marshal(p.AllowedCourses); err == nil {
		p.AllowedCoursesJSON = string(b)
	}
	if b, err := json.Marshal(p.AllowedLanguages); err == nil {
		p.AllowedLanguagesJSON = string(b)
	}
	if b, err := json.Marshal(p.AllowedAuctionTypes); err == nil {
		p.AllowedAuctionTypesJSON = string(b)
	}
	if b, err := json.Marshal(p.AllowedFormats); err == nil {
		p.AllowedFormatsJSON = string(b)
	}
	if b, err := json.Marshal(p.ReplyOverrides); err == nil {
		p.ReplyOverridesJSON = string(b)
	}
}

func (p *ClassAuctionProfile) UnmarshalJSONFields() {
	p.AllowedCourses = []string{}
	if p.AllowedCoursesJSON != "" {
		_ = json.Unmarshal([]byte(p.AllowedCoursesJSON), &p.AllowedCourses)
	}
	p.AllowedLanguages = []string{}
	if p.AllowedLanguagesJSON != "" {
		_ = json.Unmarshal([]byte(p.AllowedLanguagesJSON), &p.AllowedLanguages)
	}
	p.AllowedAuctionTypes = []string{}
	if p.AllowedAuctionTypesJSON != "" {
		_ = json.Unmarshal([]byte(p.AllowedAuctionTypesJSON), &p.AllowedAuctionTypes)
	}
	p.AllowedFormats = []string{}
	if p.AllowedFormatsJSON != "" {
		_ = json.Unmarshal([]byte(p.AllowedFormatsJSON), &p.AllowedFormats)
	}
	p.ReplyOverrides = map[string]string{}
	if p.ReplyOverridesJSON != "" {
		_ = json.Unmarshal([]byte(p.ReplyOverridesJSON), &p.ReplyOverrides)
	}
}

func (p *ClassAuctionProfile) AfterFind(_ *gorm.DB) error {
	p.UnmarshalJSONFields()
	return nil
}

func (p *ClassAuctionProfile) BeforeSave(_ *gorm.DB) error {
	p.MarshalJSONFields()
	return nil
}

// WatchedTarget defines a Telegram group (+ optional forum topic) monitored for class auctions.
type WatchedTarget struct {
	ID                 uint        `gorm:"primaryKey" json:"id"`
	AccountID          uint        `gorm:"uniqueIndex:idx_watched_target_unique;index;not null" json:"account_id"`
	GroupID            int64       `gorm:"uniqueIndex:idx_watched_target_unique;not null" json:"group_id"`
	TopicID            int         `gorm:"uniqueIndex:idx_watched_target_unique;default:0" json:"topic_id"`
	Label              string      `gorm:"type:varchar(255)" json:"label"`
	DefaultAuctionType AuctionType `gorm:"type:varchar(16);default:''" json:"default_auction_type"`
	IsActive           bool        `gorm:"default:true" json:"is_active"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

// WeeklyAvailability represents a recurring free time window in WIB for a specific weekday.
type WeeklyAvailability struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	DayOfWeek   int       `gorm:"not null" json:"day_of_week"`   // 0=Sunday .. 6=Saturday
	StartMinute int       `gorm:"not null" json:"start_minute"`  // 0..1439
	EndMinute   int       `gorm:"not null" json:"end_minute"`    // 1..1440
	IsActive    bool      `gorm:"default:true" json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// DateAvailabilityOverride represents a one-off BLOCK (busy/leave/won OTR) or EXTRA free window on a specific date.
type DateAvailabilityOverride struct {
	ID          uint             `gorm:"primaryKey" json:"id"`
	AccountID   uint             `gorm:"index;not null" json:"account_id"`
	Date        string           `gorm:"type:varchar(10);index;not null" json:"date"` // YYYY-MM-DD (WIB)
	Kind        DateOverrideKind `gorm:"type:varchar(16);not null" json:"kind"`       // BLOCK | EXTRA
	StartMinute int              `gorm:"not null" json:"start_minute"`                // 0..1439
	EndMinute   int              `gorm:"not null" json:"end_minute"`                  // 1..1440
	Reason      string           `gorm:"type:varchar(255)" json:"reason"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

// AuctionLog records every evaluated class auction message for observability and one-click schedule carving.
type AuctionLog struct {
	ID               uint            `gorm:"primaryKey" json:"id"`
	AccountID        uint            `gorm:"index;not null" json:"account_id"`
	GroupID          int64           `gorm:"index" json:"group_id"`
	TopicID          int             `json:"topic_id"`
	MessageID        int             `json:"message_id"`
	RawText          string          `gorm:"type:text" json:"raw_text"`
	ParserSource     string          `gorm:"type:varchar(32)" json:"parser_source"` // DETERMINISTIC | AI_FALLBACK
	ParseConfidence  ParseConfidence `gorm:"type:varchar(16)" json:"parse_confidence"`
	ParsedJSON       string          `gorm:"type:text" json:"-"`
	Parsed           *ParsedAuction  `gorm:"-" json:"parsed,omitempty"`
	Decision         AuctionDecision `gorm:"type:varchar(24);index" json:"decision"`
	SkipReason       string          `gorm:"type:text" json:"skip_reason,omitempty"`
	ReplyText        string          `gorm:"type:text" json:"reply_text,omitempty"`
	MatchedSlotsJSON string          `gorm:"type:text" json:"-"`
	MatchedSlots     []ScheduleSlot  `gorm:"-" json:"matched_slots,omitempty"`
	LatencyMs        int64           `json:"latency_ms"`
	Outcome          AuctionOutcome  `gorm:"type:varchar(16);default:'PENDING'" json:"outcome"`
	CreatedAt        time.Time       `gorm:"index" json:"created_at"`
}

func (l *AuctionLog) AfterFind(_ *gorm.DB) error {
	if l.ParsedJSON != "" {
		var p ParsedAuction
		if err := json.Unmarshal([]byte(l.ParsedJSON), &p); err == nil {
			l.Parsed = &p
		}
	}
	if l.MatchedSlotsJSON != "" {
		var slots []ScheduleSlot
		if err := json.Unmarshal([]byte(l.MatchedSlotsJSON), &slots); err == nil {
			l.MatchedSlots = slots
		}
	}
	return nil
}

type SimulateRequest struct {
	Text               string      `json:"text" binding:"required"`
	SimulatedNow       string      `json:"simulated_now,omitempty"`        // optional RFC3339 or YYYY-MM-DD HH:MM
	DefaultAuctionType AuctionType `json:"default_auction_type,omitempty"` // optional topic hint
	UseAIFallback      *bool       `json:"use_ai_fallback,omitempty"`      // optional override
}

type SimulateResult struct {
	ParserSource  string          `json:"parser_source"`
	ParseDuration string          `json:"parse_duration"`
	ParseMicros   int64           `json:"parse_micros"`
	Parsed        *ParsedAuction  `json:"parsed"`
	WouldBid      bool            `json:"would_bid"`
	Decision      AuctionDecision `json:"decision"`
	SkipReason    string          `json:"skip_reason,omitempty"`
	ReplyText     string          `json:"reply_text,omitempty"`
	NeedsNego     bool            `json:"needs_gender_nego"`
	MatchedSlots  []ScheduleSlot  `json:"matched_slots,omitempty"`
}

type AuctionLogFilter struct {
	Decision string
	Outcome  string
	Limit    int
	Offset   int
}

type AuctionLogPage struct {
	Items []AuctionLog `json:"items"`
	Total int64        `json:"total"`
}

type AuctionStats struct {
	TotalBidsSent   int64 `json:"total_bids_sent"`
	TotalDryRun     int64 `json:"total_dry_run"`
	TotalSkipped    int64 `json:"total_skipped"`
	TotalWon        int64 `json:"total_won"`
	BidsToday       int64 `json:"bids_today"`
	AvgLatencyMs    int64 `json:"avg_latency_ms"`
}

type ClassAuctionRepository interface {
	GetOrCreateProfile(accountID uint) (*ClassAuctionProfile, error)
	SaveProfile(profile *ClassAuctionProfile) error

	ListWatchedTargets(accountID uint) ([]WatchedTarget, error)
	CreateWatchedTarget(target *WatchedTarget) error
	UpdateWatchedTarget(target *WatchedTarget) error
	DeleteWatchedTarget(accountID uint, targetID uint) error

	ListWeeklyAvailability(accountID uint) ([]WeeklyAvailability, error)
	ReplaceWeeklyAvailability(accountID uint, windows []WeeklyAvailability) error

	ListDateOverrides(accountID uint) ([]DateAvailabilityOverride, error)
	CreateDateOverride(override *DateAvailabilityOverride) error
	DeleteDateOverride(accountID uint, overrideID uint) error

	CreateAuctionLog(log *AuctionLog) error
	FindAuctionLogByID(accountID uint, logID uint) (*AuctionLog, error)
	ListAuctionLogs(accountID uint, filter AuctionLogFilter) (*AuctionLogPage, error)
	UpdateAuctionLogOutcome(accountID uint, logID uint, outcome AuctionOutcome) error
	GetStats(accountID uint) (*AuctionStats, error)
	DeleteByAccountID(accountID uint) error
}

type ClassAuctionUseCase interface {
	// Hot path invoked by Telegram update handler
	HandleIncomingGroupMessage(ctx context.Context, accountID uint, groupID int64, topicID int, msgID int, isUserReply bool, text string, peer tg.InputPeerClass) error

	// Cache lifecycle
	InvalidateAccountCache(accountID uint)

	// Management API methods
	GetProfile(accountID uint) (*ClassAuctionProfile, error)
	UpdateProfile(accountID uint, profile *ClassAuctionProfile) (*ClassAuctionProfile, error)

	ListWatchedTargets(accountID uint) ([]WatchedTarget, error)
	AddWatchedTarget(accountID uint, target *WatchedTarget) (*WatchedTarget, error)
	UpdateWatchedTarget(accountID uint, target *WatchedTarget) (*WatchedTarget, error)
	DeleteWatchedTarget(accountID uint, targetID uint) error

	GetWeeklySchedule(accountID uint) ([]WeeklyAvailability, error)
	ReplaceWeeklySchedule(accountID uint, windows []WeeklyAvailability) ([]WeeklyAvailability, error)

	ListDateOverrides(accountID uint) ([]DateAvailabilityOverride, error)
	AddDateOverride(accountID uint, override *DateAvailabilityOverride) (*DateAvailabilityOverride, error)
	DeleteDateOverride(accountID uint, overrideID uint) error

	Simulate(ctx context.Context, accountID uint, req SimulateRequest) (*SimulateResult, error)
	ListLogs(accountID uint, filter AuctionLogFilter) (*AuctionLogPage, error)
	MarkLogOutcome(ctx context.Context, accountID uint, logID uint, outcome AuctionOutcome) (*AuctionLog, error)
	GetStats(accountID uint) (*AuctionStats, error)
}
