// Package rss parses podcast RSS feeds, notably the liveItem element
// (podcast-live standard) regardless of namespace.
package rss

import (
	"encoding/xml"
	"strings"
	"time"
)

// LiveItem mirrors the attributes of a <liveItem> element we care about.
type LiveItem struct {
	Start  string // raw start attribute
	Status string // raw status attribute (e.g. "live", "ended")
}

// FindLiveItems scans an RSS/Atom document for every liveItem element in
// any namespace.
func FindLiveItems(data []byte) []LiveItem {
	var items []LiveItem

	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	decoder.Strict = false

	depth := 0
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.EqualFold(t.Name.Local, "liveItem") {
				item := LiveItem{}
				for _, attr := range t.Attr {
					switch strings.ToLower(attr.Name.Local) {
					case "start":
						item.Start = attr.Value
					case "status":
						item.Status = attr.Value
					}
				}
				items = append(items, item)
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return items
}

// FindClosestLiveItem finds the liveItem whose start time is closest to now,
// falling back to the first item when none have a parseable start time.
func FindClosestLiveItem(items []LiveItem) (LiveItem, bool) {
	now := time.Now().UTC()
	best := -1
	var bestDelta float64

	for i, item := range items {
		if item.Start == "" {
			continue
		}
		start, err := parseTime(item.Start)
		if err != nil {
			continue
		}
		delta := start.Sub(now).Seconds()
		if delta < 0 {
			delta = -delta
		}
		if best == -1 || delta < bestDelta {
			best = i
			bestDelta = delta
		}
	}

	if best == -1 {
		if len(items) > 0 {
			return items[0], true
		}
		return LiveItem{}, false
	}
	return items[best], true
}

// parseTime parses ISO-8601 start timestamps with timezone awareness; a
// trailing "Z" is treated as +00:00.
func parseTime(s string) (time.Time, error) {
	normalized := strings.ReplaceAll(s, "Z", "+00:00")
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	var lastErr error
	for _, layout := range layouts {
		t, err := time.Parse(layout, normalized)
		if err == nil {
			return t.UTC(), nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

// IsLive reports whether the item's status attribute (case-insensitive)
// equals "live".
func (l LiveItem) IsLive() bool {
	return strings.ToLower(l.Status) == "live"
}
