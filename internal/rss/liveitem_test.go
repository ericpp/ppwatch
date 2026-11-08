package rss

import "testing"

const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:podcast="https://podcastindex.org/namespace/1.0">
  <channel>
    <title>Test Podcast</title>
    <item>
      <title>Not live</title>
    </item>
    <item>
      <title>Live item A</title>
      <podcast:liveItem status="ended" start="2024-01-01T10:00:00Z"/>
    </item>
    <item>
      <title>Live item B</title>
      <podcast:liveItem status="live" start="2024-01-01T10:05:00Z"/>
    </item>
  </channel>
</rss>`

func TestFindLiveItems(t *testing.T) {
	items := FindLiveItems([]byte(sampleFeed))
	if len(items) != 2 {
		t.Fatalf("got %d liveItems, want 2", len(items))
	}
	if items[0].Status != "ended" || items[1].Status != "live" {
		t.Errorf("statuses wrong: %+v", items)
	}
}

func TestFindClosestLiveItem(t *testing.T) {
	items := FindLiveItems([]byte(sampleFeed))
	closest, ok := FindClosestLiveItem(items)
	if !ok {
		t.Fatal("no closest item")
	}
	// 10:05 is closer to any "now" after 10:02:30 than 10:00.
	if closest.Status != "live" {
		t.Errorf("closest status = %q, want live", closest.Status)
	}
	if !closest.IsLive() {
		t.Error("IsLive() should be true")
	}
}

func TestNoLiveItems(t *testing.T) {
	items := FindLiveItems([]byte(`<rss><channel><item><title>x</title></item></channel></rss>`))
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
	if _, ok := FindClosestLiveItem(items); ok {
		t.Error("expected no closest item")
	}
}

func TestIsLiveCaseInsensitive(t *testing.T) {
	item := LiveItem{Status: "LiVe"}
	if !item.IsLive() {
		t.Error("IsLive should be case-insensitive")
	}
	item.Status = "ended"
	if item.IsLive() {
		t.Error("ended should not be live")
	}
}
