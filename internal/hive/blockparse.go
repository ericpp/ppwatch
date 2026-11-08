package hive

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// opIDRegex matches podping operation ids: any id starting with "pp_" that
// contains a second underscore.
var opIDRegex = regexp.MustCompile(`^pp_(.*)_(.*)`)

// processBlock processes a block and returns the number of updates found.
func (w *Watcher) processBlock(block map[string]any, blockNum int64) int {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Failed to process block %d: %v", blockNum, r)
		}
	}()

	updates := 0

	tsStr, _ := block["timestamp"].(string)
	if tsStr == "" {
		return 0
	}
	timestamp, err := parseHiveTime(tsStr)
	if err != nil {
		log.Printf("Failed to process block %d: %v", blockNum, err)
		return 0
	}

	transactions, _ := block["transactions"].([]any)
	txIDs, _ := block["transaction_ids"].([]any)

	for txIdx, txAny := range transactions {
		tx, ok := txAny.(map[string]any)
		if !ok {
			continue
		}
		operations, _ := tx["operations"].([]any)
		for _, opAny := range operations {
			op, ok := opAny.([]any)
			if !ok || len(op) != 2 {
				continue
			}
			opType, _ := op[0].(string)
			if opType != "custom_json" {
				continue
			}
			opData, ok := op[1].(map[string]any)
			if !ok {
				continue
			}

			opID, _ := opData["id"].(string)
			if !isPodpingOpID(opID) {
				continue
			}

			urls, medium, reason, version := parsePodpingJSON(opData)
			if len(urls) == 0 {
				continue
			}

			var trxID string
			if txIdx < len(txIDs) {
				trxID, _ = txIDs[txIdx].(string)
			}
			var account string
			auths, _ := opData["required_posting_auths"].([]any)
			if len(auths) > 0 {
				account, _ = auths[0].(string)
			}

			data := NewPodpingData(urls, timestamp, account, medium, reason, trxID, blockNum, version)

			if w.callback != nil {
				w.callback(data)
				updates += len(urls)
			}
		}
	}

	return updates
}

func isPodpingOpID(id string) bool {
	return opIDRegex.MatchString(id) || id == "podping"
}

// parsePodpingJSON parses the operation json payload, handling both
// iris (v1.1) and urls (v1.0) variants.
func parsePodpingJSON(opData map[string]any) (urls []string, medium, reason, version string) {
	jsonStr, _ := opData["json"].(string)
	var data map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		log.Printf("Failed to parse update: %v", err)
		return nil, "", "", ""
	}

	urls = podpingURLs(data)
	if len(urls) == 0 {
		return nil, "", "", ""
	}

	medium, _ = data["medium"].(string)
	reason, _ = data["reason"].(string)
	version, _ = data["version"].(string)
	return urls, medium, reason, version
}

func podpingURLs(data map[string]any) []string {
	raw, ok := data["iris"]
	if !ok || raw == nil {
		raw, ok = data["urls"]
		if !ok || raw == nil {
			return nil
		}
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		var urls []string
		for _, item := range v {
			s, ok := item.(string)
			if ok && s != "" {
				urls = append(urls, s)
			}
		}
		return urls
	default:
		return nil
	}
}

// parseHiveTime parses Hive timestamps like "2024-01-01T00:00:00" or
// RFC3339 values, treating naive timestamps as UTC.
func parseHiveTime(s string) (time.Time, error) {
	if strings.HasSuffix(s, "Z") {
		s = strings.TrimSuffix(s, "Z") + "+00:00"
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", s)
}

// toInt64 converts a decoded JSON number (float64) to int64.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
