// internal/parser/deterministic_test.go
package parser

import (
	"testing"
	"time"

	"github.com/azharf99/tele-gateway/internal/domain"
)

func TestParseDeterministic_All18RealAlgonovaSamples(t *testing.T) {
	// Tuesday, 24 Nov 2026 10:00 WIB
	now := time.Date(2026, time.November, 24, 10, 0, 0, 0, WIBLocation())
	opts := ParseOptions{
		PrivateDurationMin: 60,
		GroupDurationMin:   90,
	}

	type wantSlot struct {
		day  time.Weekday
		hour int
		min  int
		date string
	}

	tests := []struct {
		name           string
		text           string
		wantStop       bool
		wantCourse     domain.CourseCode
		wantGenericDG  bool
		wantType       domain.AuctionType
		wantFormat     domain.ClassFormat
		wantDur        int
		wantLogic      domain.ScheduleLogic
		wantSlots      []wantSlot
		wantGender     domain.Gender
		wantLang       domain.Language
		wantNoNego     bool
		wantConfidence domain.ParseConfidence
	}{
		{
			name:           "01_unity_two_slots_AND",
			text:           "Need tutor UNITY\n(FRI 20.30, SAT 12.00)\nhttps://bootcamp.amocrm.ru/leads/detail/29025198",
			wantCourse:     domain.CourseUnity,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicAND,
			wantSlots:      []wantSlot{{time.Friday, 20, 30, ""}, {time.Saturday, 12, 0, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "02_unity_private_SOLD_with_history_trap",
			text:           "Need tutor UNITY private SOLD\nKelas baru lanjutan dari AI to FE to PP2\n(SAT 12.00, FRI 20.30)\nTutor perempuan/laki2, full english\nhttps://bootcamp.amocrm.ru/leads/detail/29025198",
			wantStop:       true,
			wantCourse:     domain.CourseUnity,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicAND,
			wantSlots:      []wantSlot{{time.Saturday, 12, 0, ""}, {time.Friday, 20, 30, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageFullEnglish,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "03_ai_private_graduation_bilingual_female",
			text:           "NEED TUTOR\n\nPrivate graduation \n✨AI\nHari : Senin 19:00\nTutor Perempuan  , BILINGUAL (30% ENG 70% IND)\nhttps://bootcamp.amocrm.ru/leads/detail/28743714 (https://bootcamp.amocrm.ru/leads/detail/28698058)",
			wantCourse:     domain.CourseAI,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Monday, 19, 0, ""}},
			wantGender:     domain.GenderFemale,
			wantLang:       domain.LanguageBilingual,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "04_generic_graphic_design_with_start_date_and_note",
			text:           "NEED TUTOR PRIVATE NEW\n\nGraphic Design\n10 y.o\nSelasa pukul 19:30 WIB, Start 8 Desember 2026\ntutor perempuan\nNote : \n- kak ini anak ADHD tapi masih bisa diajak komunikasi dengan baik\n- anak super aktif, kasi tutor yang seru, sabar & menyenangkan ya\n\nAmo CRM : https://bootcamp.amocrm.ru/leads/detail/30489389",
			wantCourse:     domain.CourseDGJ,
			wantGenericDG:  true,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Tuesday, 19, 30, "2026-12-08"}},
			wantGender:     domain.GenderFemale,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "05_ps1_after_grad_ai_trap_and_invisible_word_joiner",
			text:           "NEED TUTOR\n\nPRIVATE NEW (AFTER GRAD AI)\n\n\nPS 1\n✨\u2060JUMAT 15.00 WIB \n✨27 NOV 2026\n✨ Tutor PEREMPUAN\n✨ Bahasa  BEBAS \nhttps://bootcamp.amocrm.ru/leads/detail/29507216 (https://bootcamp.amocrm.ru/leads/detail/30389470)",
			wantCourse:     domain.CoursePS1,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Friday, 15, 0, "2026-11-27"}},
			wantGender:     domain.GenderFemale,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "06_ps1_full_english_no_indo",
			text:           "NEED TUTOR\n\nPrivate PS1 mulai L2 full english (anak tidak bisa indo)\nRabu pukul 20:00 WIB\nadakah adakah\nhttps://bootcamp.amocrm.ru/leads/detail/27833530",
			wantCourse:     domain.CoursePS1,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Wednesday, 20, 0, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageFullEnglish,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:          "07_dg_junior_OR_request_WIT_to_WIB_and_holiday_note_trap",
			text:          "IND 1-1 DG 12235 (WED 14.00) https://lms.algonova.id/group/view/2128361 Wednesday  14:00  Alif Rizqi Ramadhan  15-Jul-2026 NEED TUTOR PRIVATE DG JUNIOR 📍GRAP DES JR\n\nhttps://bootcamp.amocrm.ru/leads/detail/30076350\n\n jadwal : \nREQUEST : SENIN  / RABU  / SABTU 16.00 WIT = 14:00 WIB / MINGGU 19.00 WIT = 17:00 WIB\n\n note : \n- bahasa indonesia\n- ANAK TINGGAL DI AMBON ( WIT ), \n- anak sangat tertarik grapdes, silakan pilihkan tutor yang seru, sabar, karena anak suka ngobrol. male/female aman gpp..\n- mama ada harapan bisa mulai segera, \nkalau bisa di masa liburan ini bisa segera dimulai, kalau bisa, mau WEEKDAY 10.00 WIT = 08.00 WIB ( hanya selama liburan ), silakan bantu FU ya kak mengenai hal ini ke tutor",
			wantCourse:    domain.CourseDGJ,
			wantGenericDG: false,
			wantType:      domain.AuctionTypeNew,
			wantFormat:    domain.ClassFormatPrivate,
			wantDur:       60,
			wantLogic:     domain.ScheduleLogicOR,
			wantSlots: []wantSlot{
				{time.Monday, 14, 0, ""},
				{time.Wednesday, 14, 0, ""},
				{time.Saturday, 14, 0, ""},
				{time.Sunday, 17, 0, ""},
			},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageIndonesia,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "08_vp_OR_jumat_atau_sabtu",
			text:           "NEED TUTOR PRIVATE\nVP\nJumat atau Sabtu pkl. 19:00 WIB (1 kali seminggu)\nPerempuan\nFull English\n\nhttps://bootcamp.amocrm.ru/leads/detail/28851022",
			wantCourse:     domain.CourseVP,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicOR,
			wantSlots:      []wantSlot{{time.Friday, 19, 0, ""}, {time.Saturday, 19, 0, ""}},
			wantGender:     domain.GenderFemale,
			wantLang:       domain.LanguageFullEnglish,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "09_dgs_private_bilingual_female",
			text:           "NEED TUTOR\n\nPrivate DGS perempuan bilingual\nsabtu jam 10:00 \nhttps://bootcamp.amocrm.ru/leads/detail/30060470\nadakah yg bisa",
			wantCourse:     domain.CourseDGS,
			wantType:       domain.AuctionTypeNew,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Saturday, 10, 0, ""}},
			wantGender:     domain.GenderFemale,
			wantLang:       domain.LanguageBilingual,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "10_otr_urgent_without_tutor_word_and_VP_to_PS_transition",
			text:           "Need OTR Urgent\nhttps://lms.algonova.id/group/view/1731773\nIND 1-1 VP to PS 12102 (MON 18.00)\nLesson 23\nMohon bantuannya teman-teman",
			wantCourse:     domain.CoursePS1,
			wantType:       domain.AuctionTypeOTR,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Monday, 18, 0, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "11_otr_tutor_sakit_hari_ini_CK_group_prem",
			text:           "NEED TUTOR OTR\nTutor Sakit \n\nHari ini, 19.00 WIB\nCK Group PREM\nLesson 26\n\nhttps://lms.algonova.id/group/view/2038808",
			wantCourse:     domain.CourseCK,
			wantType:       domain.AuctionTypeOTR,
			wantFormat:     domain.ClassFormatGroup,
			wantDur:        90,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Tuesday, 19, 0, "2026-11-24"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "12_otr_cuti_ps2_prem_explicit_date",
			text:           "NEED OTR CUTI \nPS2 PREM\nMinggu, 29 Nov 2026 Pukul 10.00\nLesson 51. M4 P1. Dasar-dasar Pemrosesan Gambar \nada yg bisa bantu take kakak2?",
			wantCourse:     domain.CoursePS2,
			wantType:       domain.AuctionTypeOTR,
			wantFormat:     domain.ClassFormatGroup,
			wantDur:        90,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Sunday, 10, 0, "2026-11-29"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "13_otr_lms_group_code_RB_without_1_1_and_hari_ini",
			text:           "Need tutor OTR\n\nIND-RB-401-MON-19.00\nLesson 14\nHari ini jam 19.00 WIB\n\nada yang bisa bantuk take kakak kakak?",
			wantCourse:     domain.CourseRB,
			wantType:       domain.AuctionTypeOTR,
			wantFormat:     domain.ClassFormatGroup,
			wantDur:        90,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Tuesday, 19, 0, "2026-11-24"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "14_otr_ps_prem_time_before_hari_ini",
			text:           "need tutor OTR\n\nPS Prem lesson 33\n19.00 WIB hari ini",
			wantCourse:     domain.CoursePS1,
			wantType:       domain.AuctionTypeOTR,
			wantFormat:     domain.ClassFormatGroup,
			wantDur:        90,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Tuesday, 19, 0, "2026-11-24"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "15_fr_ai_prem_rabu_1900",
			text:           "Need tutor FR\nAI Prem\nRabu 19.00\nLesson 17\nKalau bisa start besok\nApa ada yang bisa bantu kakak\"? Karena di hari Rabu saya akan ada kegiatan rutin. Terima kasih 🙏🏻",
			wantCourse:     domain.CourseAI,
			wantType:       domain.AuctionTypeFR,
			wantFormat:     domain.ClassFormatGroup,
			wantDur:        90,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Wednesday, 19, 0, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageAny,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "16_forever_replacement_dl_private_no_nego",
			text:           "NEED TUTOR FOREVER REPLACEMENT\n\nIND 1-1 DL 413 (WED 18.30) <— NO NEGO\nAnnasya - 8tahun\nAnaknya aktif dan kreatif, Parents sangat kooperatif\nBahasa Indonesia\n\nReason: tabrakan waktu\nLet's gooo",
			wantCourse:     domain.CourseDL,
			wantType:       domain.AuctionTypeFR,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Wednesday, 18, 30, ""}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageIndonesia,
			wantNoNego:     true,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "17_catch_up_1x_graphic_design_junior_with_next_line_date",
			text:           "NEED TUTOR \n\nCATCH UP 1X KLS PRIVATE DURASI 60 MENIT \n\nGRAPHIC DESIGN JUNIOR \n✨\u2060SABTU 17.00 WIB\n✨28 NOV 2026\n✨ Tutor BEBAS\n✨ Bahasa  INDO \nhttps://bootcamp.amocrm.ru/leads/detail/30453245 (https://bootcamp.amocrm.ru/leads/detail/30389470)",
			wantCourse:     domain.CourseDGJ,
			wantGenericDG:  false,
			wantType:       domain.AuctionTypeCU,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Saturday, 17, 0, "2026-11-28"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageIndonesia,
			wantConfidence: domain.ConfidenceHigh,
		},
		{
			name:           "18_cu_1x_dgs_date_without_weekday_name",
			text:           "NEED TUTOR\nCU 1x \nDGS, Lesson 1\n\nJadwal, Tgl 24 Nov 2026, jam 19.00\nTutor bebas\nBahasa indonesia\n\nhttps://bootcamp.amocrm.ru/leads/detail/30496439\nhttps://bootcamp.amocrm.ru/leads/detail/30490367",
			wantCourse:     domain.CourseDGS,
			wantType:       domain.AuctionTypeCU,
			wantFormat:     domain.ClassFormatPrivate,
			wantDur:        60,
			wantLogic:      domain.ScheduleLogicSingle,
			wantSlots:      []wantSlot{{time.Tuesday, 19, 0, "2026-11-24"}},
			wantGender:     domain.GenderAny,
			wantLang:       domain.LanguageIndonesia,
			wantConfidence: domain.ConfidenceHigh,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDeterministic(tc.text, now, opts)
			if !got.IsAuctionPost {
				t.Fatalf("IsAuctionPost = false, want true")
			}
			if got.HasStopKeyword != tc.wantStop {
				t.Errorf("HasStopKeyword = %v, want %v", got.HasStopKeyword, tc.wantStop)
			}
			if got.Course != tc.wantCourse {
				t.Errorf("Course = %q, want %q", got.Course, tc.wantCourse)
			}
			if got.IsGenericDG != tc.wantGenericDG {
				t.Errorf("IsGenericDG = %v, want %v", got.IsGenericDG, tc.wantGenericDG)
			}
			if got.AuctionType != tc.wantType {
				t.Errorf("AuctionType = %q, want %q", got.AuctionType, tc.wantType)
			}
			if got.ClassFormat != tc.wantFormat {
				t.Errorf("ClassFormat = %q, want %q", got.ClassFormat, tc.wantFormat)
			}
			if got.DurationMin != tc.wantDur {
				t.Errorf("DurationMin = %d, want %d", got.DurationMin, tc.wantDur)
			}
			if got.ScheduleLogic != tc.wantLogic {
				t.Errorf("ScheduleLogic = %q, want %q (slots=%+v)", got.ScheduleLogic, tc.wantLogic, got.Schedules)
			}
			if len(got.Schedules) != len(tc.wantSlots) {
				t.Fatalf("len(Schedules) = %d (%+v), want %d", len(got.Schedules), got.Schedules, len(tc.wantSlots))
			}
			for i, ws := range tc.wantSlots {
				s := got.Schedules[i]
				wantMin := ws.hour*60 + ws.min
				if s.DayOfWeek != int(ws.day) || s.StartMinute != wantMin || s.SpecificDate != ws.date {
					t.Errorf("slot[%d] = (day=%d min=%d date=%q), want (day=%d min=%d date=%q)",
						i, s.DayOfWeek, s.StartMinute, s.SpecificDate, int(ws.day), wantMin, ws.date)
				}
			}
			if got.RequiredGender != tc.wantGender {
				t.Errorf("RequiredGender = %q, want %q", got.RequiredGender, tc.wantGender)
			}
			if got.Language != tc.wantLang {
				t.Errorf("Language = %q, want %q", got.Language, tc.wantLang)
			}
			if got.NoNego != tc.wantNoNego {
				t.Errorf("NoNego = %v, want %v", got.NoNego, tc.wantNoNego)
			}
			if got.Confidence != tc.wantConfidence {
				t.Errorf("Confidence = %q, want %q (notes=%v)", got.Confidence, tc.wantConfidence, got.Notes)
			}
		})
	}
}

func TestParseDeterministic_NonAuctionAndStandaloneTimezones(t *testing.T) {
	now := time.Date(2026, time.November, 24, 10, 0, 0, 0, WIBLocation())
	opts := ParseOptions{}

	nonAuction := ParseDeterministic("Me kak, bisa ambil yang hari Jumat!", now, opts)
	if nonAuction.IsAuctionPost {
		t.Error("reply message must not be detected as an auction post")
	}

	wita := ParseDeterministic("Need tutor FE\nSabtu 20.00 WITA\nTutor laki-laki", now, opts)
	if wita.Course != domain.CourseFE || len(wita.Schedules) != 1 || wita.Schedules[0].StartMinute != 19*60 || wita.RequiredGender != domain.GenderMale {
		t.Fatalf("unexpected WITA parse result: %+v", wita)
	}
}

func BenchmarkParseDeterministic(b *testing.B) {
	now := time.Date(2026, time.November, 24, 10, 0, 0, 0, WIBLocation())
	opts := ParseOptions{PrivateDurationMin: 60, GroupDurationMin: 90}
	sample := "Need tutor UNITY private\nKelas baru lanjutan dari AI to FE to PP2\n(SAT 12.00, FRI 20.30)\nTutor perempuan/laki2, full english\nhttps://bootcamp.amocrm.ru/leads/detail/29025198"

	for b.Loop() {
		_ = ParseDeterministic(sample, now, opts)
	}
}
