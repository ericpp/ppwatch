package hive

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// newFakeRPCServer spins up an httptest server answering Hive JSON-RPC calls.
func newFakeRPCServer(t *testing.T, headBlock int64, blockFor func(int64) map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("bad request: %v", err)
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": 1}
		switch req.Method {
		case "condenser_api.get_dynamic_global_properties":
			resp["result"] = map[string]any{"head_block_number": headBlock}
		case "condenser_api.get_block":
			num, _ := req.Params[0].(float64)
			resp["result"] = blockFor(int64(num))
		default:
			resp["error"] = map[string]any{"code": -1, "message": "unknown method " + req.Method}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func podpingBlock(blockNum int64, trxID, opID, opJSON string) map[string]any {
	return map[string]any{
		"timestamp":       "2024-01-01T00:00:00",
		"transaction_ids": []any{trxID},
		"transactions": []any{
			map[string]any{
				"operations": []any{
					[]any{"custom_json", map[string]any{
						"id":                     opID,
						"required_posting_auths": []any{"alice"},
						"json":                   opJSON,
					}},
				},
			},
		},
	}
}

func TestWatcherProcessesPodping(t *testing.T) {
	opJSON := `{"version":"1.1","medium":"podcast","reason":"live","iris":["https://example.com/feed.xml"]}`
	block := podpingBlock(1002, "trx1", "pp_podcast_live", opJSON)

	srv := newFakeRPCServer(t, 1002, func(n int64) map[string]any {
		if n == 1002 {
			return block
		}
		return nil
	})

	w := NewWatcher([]string{srv.URL})
	w.running = true

	var mu sync.Mutex
	var got []PodpingData
	w.OnUpdate(func(d PodpingData) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	})

	w.processBlocks(context.Background(), NewHiveClient([]string{srv.URL}))

	mu.Lock()
	defer mu.Unlock()
	if cb, ok := w.CurrentBlock(); !ok || cb != 1003 {
		t.Fatalf("current block = %v/%v, want 1003", cb, ok)
	}
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	d := got[0]
	if len(d.URLs) != 1 || d.URLs[0] != "https://example.com/feed.xml" {
		t.Errorf("urls = %v", d.URLs)
	}
	if d.Reason != "live" || d.Account != "alice" || d.TrxID != "trx1" || d.BlockNum != 1002 {
		t.Errorf("unexpected data: %+v", d)
	}
	if d.Version != "1.1" || d.Medium != "podcast" {
		t.Errorf("unexpected version/medium: %s/%s", d.Version, d.Medium)
	}
}

func TestWatcherNullBlockAndV1URLs(t *testing.T) {
	// Block 1000 returns null; block 1001 uses the v1.0 "urls" field with a
	// bare string iris value.
	opJSON := `{"version":"1.0","urls":"https://example.net/old.xml"}`
	srv := newFakeRPCServer(t, 1001, func(n int64) map[string]any {
		if n == 1001 {
			return podpingBlock(1001, "trx2", "podping", opJSON)
		}
		return nil
	})

	w := NewWatcher([]string{srv.URL})
	w.running = true

	var mu sync.Mutex
	var got []PodpingData
	w.OnUpdate(func(d PodpingData) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	})

	w.processBlocks(context.Background(), NewHiveClient([]string{srv.URL}))

	mu.Lock()
	defer mu.Unlock()
	if cb, ok := w.CurrentBlock(); !ok || cb != 1002 {
		t.Fatalf("current block = %v/%v, want 1002", cb, ok)
	}
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	if got[0].URLs[0] != "https://example.net/old.xml" {
		t.Errorf("urls = %v", got[0].URLs)
	}
	if got[0].Version != "1.0" {
		t.Errorf("version = %s, want 1.0", got[0].Version)
	}
}

func TestIsPodpingOpID(t *testing.T) {
	cases := map[string]bool{
		"pp_podcast_update": true,
		"pp_music_live":     true,
		"podping":           true,
		"pp_podping":        false, // only one underscore; "podping$" requires the whole id
		"x_podping":         false, // re.match anchors the whole pattern at position 0
		"follow":            false,
		"custom_id":         false,
	}

	for id, want := range cases {
		if got := isPodpingOpID(id); got != want {
			t.Errorf("isPodpingOpID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestParseHiveTime(t *testing.T) {
	for _, s := range []string{"2024-01-01T00:00:00", "2024-01-01T00:00:00Z"} {
		ts, err := parseHiveTime(s)
		if err != nil {
			t.Fatalf("parseHiveTime(%q) failed: %v", s, err)
		}
		if ts.IsZero() || ts.Unix() != 1704067200 {
			t.Errorf("parseHiveTime(%q) = %v", s, ts)
		}
	}
	if !strings.Contains("abc", "b") {
		t.Fatal("strings sanity check failed")
	}
}
