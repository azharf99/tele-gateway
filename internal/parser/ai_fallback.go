// internal/parser/ai_fallback.go
package parser

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
	"go.uber.org/zap"
	"google.golang.org/genai"
)

const aiFallbackTimeout = 3 * time.Second

// AIFallbackParser uses Gemini structured JSON output as an optional secondary
// parser ONLY when the deterministic parser returns ConfidenceLow.
type AIFallbackParser struct {
	client *genai.Client
	model  string
	logger *zap.Logger
}

func NewAIFallbackParser(apiKey string, logger *zap.Logger) (*AIFallbackParser, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, nil
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("init gemini fallback parser: %w", err)
	}

	aimodel := strings.TrimSpace(os.Getenv("AI_FALLBACK_MODEL"))

	if aimodel == "" {
		aimodel = "gemini-3.1-flash-lite"
	}

	return &AIFallbackParser{
		client: client,
		model:  aimodel,
		logger: logger,
	}, nil
}

type aiParsedDTO struct {
	IsAuctionPost  bool   `json:"is_auction_post"`
	HasStopKeyword bool   `json:"has_stop_keyword"`
	Course         string `json:"course"`
	IsGenericDG    bool   `json:"is_generic_dg"`
	AuctionType    string `json:"auction_type"`
	ClassFormat    string `json:"class_format"`
	DurationMin    int    `json:"duration_min"`
	ScheduleLogic  string `json:"schedule_logic"`
	Schedules      []struct {
		DayOfWeek    int    `json:"day_of_week"`
		StartMinute  int    `json:"start_minute"`
		SpecificDate string `json:"specific_date"`
	} `json:"schedules"`
	RequiredGender string `json:"required_gender"`
	Language       string `json:"language"`
	NoNego         bool   `json:"no_nego"`
}

func (p *AIFallbackParser) Parse(ctx context.Context, rawText string, now time.Time, opts ParseOptions) (*domain.ParsedAuction, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("AI fallback parser not configured")
	}
	if now.IsZero() {
		now = time.Now().In(WIBLocation())
	} else {
		now = now.In(WIBLocation())
	}

	reqCtx, cancel := context.WithTimeout(ctx, aiFallbackTimeout)
	defer cancel()

	systemPrompt := fmt.Sprintf(`You extract Algonova Indonesia coding class auction messages into strict JSON.
Current WIB datetime: %s (weekday=%d where 0=Sunday..6=Saturday).
Course codes (use ONLY one of these exact codes or empty string if unknown):
UNITY, PS1, PS2, PP1, PP2, FE, DGS, DGJ, VP, CK, DL, AI, RB.
Important rules:
- Ignore student progression history like "lanjutan dari AI to FE to PP2" or "AFTER GRAD AI"; extract the NEW course being taught now. For "VP to PS", extract PS1.
- "Graphic Design" without Junior/Senior -> course="DGJ" and is_generic_dg=true.
- auction_type: NEW, OTR (One Time Replacement / cuti / sakit), CU (Catch Up / Extra Class / EC), or FR (Forever Replacement).
- class_format: PRIVATE (60 min) or GROUP (90 min, when PREM/Grup or LMS code IND-XX-NNN without 1-1).
- Convert WITA (-1h) and WIT (-2h) to WIB.
- schedules[].day_of_week: 0=Sunday..6=Saturday; start_minute: hour*60+minute in WIB (0..1439); specific_date: YYYY-MM-DD or "".
- schedule_logic: SINGLE (1 slot), AND (must teach all slots), OR (pick one option).
- required_gender: ANY, MALE, or FEMALE.
- language: ANY, INDONESIA, BILINGUAL, or FULL_ENGLISH.
- has_stop_keyword: true if message says SOLD, CLOSED, TAKEN, DONE, or BATAL.`,
		now.Format("2006-01-02 15:04 MST"), int(now.Weekday()))

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(systemPrompt, ""),
		ResponseMIMEType:  "application/json",
		Temperature:       genai.Ptr(float32(0)),
	}

	resp, err := p.client.Models.GenerateContent(reqCtx, p.model, genai.Text(rawText), cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini fallback error: %w", err)
	}

	var payload string
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			payload += part.Text
		}
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("gemini fallback returned empty response")
	}

	var dto aiParsedDTO
	if err := json.Unmarshal([]byte(payload), &dto); err != nil {
		return nil, fmt.Errorf("unmarshal gemini fallback json: %w", err)
	}

	out := &domain.ParsedAuction{
		IsAuctionPost:  dto.IsAuctionPost,
		HasStopKeyword: dto.HasStopKeyword,
		Course:         domain.CourseCode(strings.ToUpper(strings.TrimSpace(dto.Course))),
		IsGenericDG:    dto.IsGenericDG,
		AuctionType:    domain.AuctionType(strings.ToUpper(strings.TrimSpace(dto.AuctionType))),
		ClassFormat:    domain.ClassFormat(strings.ToUpper(strings.TrimSpace(dto.ClassFormat))),
		DurationMin:    dto.DurationMin,
		ScheduleLogic:  domain.ScheduleLogic(strings.ToUpper(strings.TrimSpace(dto.ScheduleLogic))),
		RequiredGender: domain.Gender(strings.ToUpper(strings.TrimSpace(dto.RequiredGender))),
		Language:       domain.Language(strings.ToUpper(strings.TrimSpace(dto.Language))),
		NoNego:         dto.NoNego,
		Schedules:      make([]domain.ScheduleSlot, 0, len(dto.Schedules)),
	}

	if !domain.IsValidCourseCode(out.Course) {
		out.Course = ""
	}
	if out.ClassFormat != domain.ClassFormatGroup {
		out.ClassFormat = domain.ClassFormatPrivate
	}
	if out.DurationMin <= 0 {
		if out.ClassFormat == domain.ClassFormatGroup {
			out.DurationMin = opts.GroupDurationMin
			if out.DurationMin <= 0 {
				out.DurationMin = 90
			}
		} else {
			out.DurationMin = opts.PrivateDurationMin
			if out.DurationMin <= 0 {
				out.DurationMin = 60
			}
		}
	}
	if out.RequiredGender == "" {
		out.RequiredGender = domain.GenderAny
	}
	if out.Language == "" {
		out.Language = domain.LanguageAny
	}
	if out.AuctionType == "" {
		if opts.DefaultAuctionType != "" {
			out.AuctionType = opts.DefaultAuctionType
		} else {
			out.AuctionType = domain.AuctionTypeNew
		}
	}

	for _, s := range dto.Schedules {
		if s.DayOfWeek < 0 || s.DayOfWeek > 6 || s.StartMinute < 0 || s.StartMinute >= 1440 {
			continue
		}
		out.Schedules = append(out.Schedules, domain.ScheduleSlot{
			DayOfWeek:    s.DayOfWeek,
			StartMinute:  s.StartMinute,
			SpecificDate: strings.TrimSpace(s.SpecificDate),
		})
	}
	if len(out.Schedules) == 1 {
		out.ScheduleLogic = domain.ScheduleLogicSingle
	} else if len(out.Schedules) > 1 && out.ScheduleLogic != domain.ScheduleLogicOR {
		out.ScheduleLogic = domain.ScheduleLogicAND
	}

	if out.Course != "" && len(out.Schedules) > 0 {
		out.Confidence = domain.ConfidenceMedium
	} else {
		out.Confidence = domain.ConfidenceLow
	}

	if urls := reURL.FindAllString(rawText, -1); len(urls) > 0 {
		out.LeadURL = urls[0]
	}

	return out, nil
}
