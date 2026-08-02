// internal/delivery/http/csv_import_test.go
package http

import (
	"strings"
	"testing"
)

func TestParseBidRuleCSV_ValidWithQuotedCommasAndDefaults(t *testing.T) {
	// Row 1: keyword contains commas (the AND-list) and must be quoted; optional
	// columns present. Row 2: only required columns supplied → defaults apply.
	csv := "target_group_id,topic_id,keyword,bid_message,stop_keywords,is_active,has_bidded\n" +
		`123456789,5,"pa/a, 512gb, pristine","Bid 5jt","sold,closed",true,false` + "\n" +
		"987654321,,iphone,\"OB\",,,\n"

	rules, rowErrors, err := parseBidRuleCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rowErrors) != 0 {
		t.Fatalf("expected no row errors, got %v", rowErrors)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	r0 := rules[0]
	if r0.TargetGroupID != 123456789 || r0.TopicID != 5 {
		t.Errorf("row0 ids wrong: group=%d topic=%d", r0.TargetGroupID, r0.TopicID)
	}
	if r0.Keyword != "pa/a, 512gb, pristine" {
		t.Errorf("row0 keyword not preserved through quoting: %q", r0.Keyword)
	}
	if r0.StopKeywords != "sold,closed" {
		t.Errorf("row0 stop_keywords wrong: %q", r0.StopKeywords)
	}
	if !r0.IsActive || r0.HasBidded {
		t.Errorf("row0 flags wrong: active=%v bidded=%v", r0.IsActive, r0.HasBidded)
	}

	r1 := rules[1]
	if r1.TopicID != 0 {
		t.Errorf("row1 topic_id should default to 0, got %d", r1.TopicID)
	}
	if !r1.IsActive { // default true
		t.Errorf("row1 is_active should default to true")
	}
	if r1.HasBidded { // default false
		t.Errorf("row1 has_bidded should default to false")
	}
}

func TestParseBidRuleCSV_MissingRequiredColumn(t *testing.T) {
	csv := "target_group_id,keyword\n123,iphone\n" // no bid_message column
	_, _, err := parseBidRuleCSV(strings.NewReader(csv))
	if err == nil {
		t.Fatal("expected error for missing required column")
	}
	if !strings.Contains(err.Error(), "bid_message") {
		t.Errorf("error should name the missing column, got: %v", err)
	}
}

func TestParseBidRuleCSV_RowLevelValidation(t *testing.T) {
	csv := "target_group_id,keyword,bid_message\n" +
		"notanumber,iphone,OB\n" + // bad group id -> row error
		",iphone,OB\n" + // missing required field -> row error
		"555,ipad,Bid\n" // valid

	rules, rowErrors, err := parseBidRuleCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 valid rule, got %d", len(rules))
	}
	if len(rowErrors) != 2 {
		t.Fatalf("expected 2 row errors, got %d: %v", len(rowErrors), rowErrors)
	}
	// Header is row 1, so the bad-group row is row 2.
	if rowErrors[0].Row != 2 {
		t.Errorf("expected first error on row 2, got %d", rowErrors[0].Row)
	}
}

func TestParseBidRuleCSV_HeaderOrderIndependenceAndEmptyLines(t *testing.T) {
	csv := "keyword,bid_message,target_group_id\n" +
		"\n" + // blank line tolerated
		"iphone,OB,42\n"

	rules, rowErrors, err := parseBidRuleCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rowErrors) != 0 {
		t.Fatalf("expected no row errors, got %v", rowErrors)
	}
	if len(rules) != 1 || rules[0].TargetGroupID != 42 || rules[0].Keyword != "iphone" {
		t.Fatalf("column-order-independent parse failed: %+v", rules)
	}
}
