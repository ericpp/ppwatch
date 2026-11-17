package bot

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ericpp/ppwatch/internal/config"
)

// endedFeedXML is a minimal feed whose only liveItem has status="ended".
const endedFeedXML = `<?xml version="1.0"?>
<rss version="2.0" xmlns:podcast="https://podcastindex.org/namespace/1.0">
  <channel>
    <title>Test Podcast</title>
    <item>
      <title>Live item</title>
      <podcast:liveItem status="ended" start="2024-01-01T10:00:00Z"/>
    </item>
  </channel>
</rss>`

func newLiveTestBot(srv *httptest.Server, retries int, delay float64) *Bot {
	b := New(&config.BotConfig{
		APITimeout:           2.0,
		LiveVerifyRetries:    retries,
		LiveVerifyRetryDelay: delay,
	})
	b.http = srv.Client()
	return b
}

func TestCheckLiveItemStatusRetriesOnHTTP500(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) <= 2 {
			// Feed is still being regenerated on the host.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(endedFeedXML))
	}))
	defer srv.Close()

	b := newLiveTestBot(srv, 3, 0.01)

	live, errMsg := b.CheckLiveItemStatus(srv.URL)
	if live != 0 {
		t.Errorf("live = %d, want 0 (errMsg = %q)", live, errMsg)
	}
	if errMsg != "" {
		t.Errorf("errMsg = %q, want empty", errMsg)
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("server saw %d requests, want 3 (1 attempt + 2 retries)", got)
	}
}

func TestCheckLiveItemStatusExhaustsRetries(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	b := newLiveTestBot(srv, 2, 0.01)

	live, errMsg := b.CheckLiveItemStatus(srv.URL)
	if live != -1 {
		t.Errorf("live = %d, want -1", live)
	}
	if !strings.Contains(errMsg, "HTTP 500") {
		t.Errorf("errMsg = %q, want it to mention HTTP 500", errMsg)
	}
	if got := requests.Load(); got != 3 {
		t.Errorf("server saw %d requests, want 3 (1 attempt + 2 retries)", got)
	}
}

func TestCheckLiveItemStatusNoRetryOnHTTP404(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	b := newLiveTestBot(srv, 3, 0.01)

	live, errMsg := b.CheckLiveItemStatus(srv.URL)
	if live != -1 {
		t.Errorf("live = %d, want -1", live)
	}
	if !strings.Contains(errMsg, "HTTP 404") {
		t.Errorf("errMsg = %q, want it to mention HTTP 404", errMsg)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1 (404 is not retried)", got)
	}
}

func TestCheckLiveItemStatusLiveItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(strings.Replace(endedFeedXML, `status="ended"`, `status="live"`, 1)))
	}))
	defer srv.Close()

	b := newLiveTestBot(srv, 0, 0)

	live, errMsg := b.CheckLiveItemStatus(srv.URL)
	if live != 1 {
		t.Errorf("live = %d, want 1 (errMsg = %q)", live, errMsg)
	}
}
