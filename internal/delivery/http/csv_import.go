// internal/delivery/http/csv_import.go
package http

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/azharf99/tele-gateway/internal/domain"
)

const (
	// maxImportRows caps how many data rows a single import may contain, to bound
	// memory and DB work regardless of the (already size-limited) upload.
	maxImportRows = 1000
	// maxImportBytes limits the raw upload size to mitigate memory-exhaustion DoS.
	maxImportBytes = 2 << 20 // 2 MiB
)

// RowError describes why a single CSV data row was rejected. Row numbers are
// 1-based and include the header row (so the first data row is row 2).
type RowError struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// parseBidRuleCSV reads a CSV stream and returns the valid BidRules together with
// per-row errors for the rows that were skipped. The first record must be a header
// row; columns are matched by name (case-insensitive), so column order is flexible
// and optional columns may be omitted entirely.
//
//	Required : target_group_id, keyword, bid_message
//	Optional : topic_id (default 0), stop_keywords (default ""),
//	           is_active (default true), has_bidded (default false)
//
// encoding/csv is used deliberately: the keyword field is itself a comma-separated
// AND-list and bid_message may contain commas, so quoted fields ("pa/a, 512gb")
// must be parsed correctly rather than naively split on ",".
func parseBidRuleCSV(r io.Reader) ([]domain.BidRule, []RowError, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // columns are mapped by header name, not position
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, nil, fmt.Errorf("CSV is empty")
		}
		return nil, nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	col := make(map[string]int, len(header))
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}

	for _, name := range []string{"target_group_id", "keyword", "bid_message"} {
		if _, ok := col[name]; !ok {
			return nil, nil, fmt.Errorf("missing required column: %q", name)
		}
	}

	get := func(record []string, name string) string {
		idx, ok := col[name]
		if !ok || idx >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[idx])
	}

	var (
		rules     []domain.BidRule
		rowErrors []RowError
		rowNum    = 1 // header consumed above
	)

	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		rowNum++

		if readErr != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Message: "malformed CSV row: " + readErr.Error()})
			continue
		}

		if len(rules)+len(rowErrors) >= maxImportRows {
			return nil, nil, fmt.Errorf("too many rows (limit %d)", maxImportRows)
		}

		if isEmptyRecord(record) {
			continue // tolerate blank lines
		}

		groupStr := get(record, "target_group_id")
		keyword := get(record, "keyword")
		bidMsg := get(record, "bid_message")

		if groupStr == "" || keyword == "" || bidMsg == "" {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Message: "target_group_id, keyword and bid_message are required"})
			continue
		}

		groupID, convErr := strconv.ParseInt(groupStr, 10, 64)
		if convErr != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Message: fmt.Sprintf("invalid target_group_id %q (must be an integer)", groupStr)})
			continue
		}

		topicID := 0
		if s := get(record, "topic_id"); s != "" {
			topicID, convErr = strconv.Atoi(s)
			if convErr != nil {
				rowErrors = append(rowErrors, RowError{Row: rowNum, Message: fmt.Sprintf("invalid topic_id %q (must be an integer)", s)})
				continue
			}
		}

		rules = append(rules, domain.BidRule{
			TargetGroupID: groupID,
			TopicID:       topicID,
			Keyword:       keyword,
			BidMessage:    bidMsg,
			StopKeywords:  get(record, "stop_keywords"),
			IsActive:      parseBool(get(record, "is_active"), true),
			HasBidded:     parseBool(get(record, "has_bidded"), false),
		})
	}

	return rules, rowErrors, nil
}

func isEmptyRecord(record []string) bool {
	for _, f := range record {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// parseBool interprets common truthy/falsy spellings, returning def for blank or
// unrecognized input so an odd cell never silently flips a rule's state.
func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y", "active":
		return true
	case "0", "false", "no", "n", "paused", "inactive":
		return false
	default:
		return def
	}
}
