package hive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// countingServer wraps an httptest server and counts received requests.
type countingServer struct {
	*httptest.Server
	requests atomic.Int32
}

// newEmptyResultServer returns a server that always answers with a
// well-formed JSON-RPC envelope that has no result field, mimicking
// overloaded hived/jussi nodes.
func newEmptyResultServer() *countingServer {
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1})
	}))
	return cs
}

// newMalformedResultServer returns a server whose result payload is a JSON
// number, which fails to unmarshal into the map-based getters.
func newMalformedResultServer() *countingServer {
	cs := &countingServer{}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": 123, "id": 1})
	}))
	return cs
}

func TestRPCCallRotatesPastEmptyResult(t *testing.T) {
	bad := newEmptyResultServer()
	defer bad.Close()
	good := newFakeRPCServer(t, 100, func(int64) map[string]any { return nil })

	c := NewHiveClient([]string{bad.URL, good.URL})
	result, err := c.RPCCall(context.Background(), "condenser_api.get_dynamic_global_properties", nil)
	if err != nil {
		t.Fatalf("RPCCall failed: %v", err)
	}
	var props map[string]any
	if err := json.Unmarshal(result, &props); err != nil {
		t.Fatalf("result is not an object: %v", err)
	}
	if props["head_block_number"] != float64(100) {
		t.Errorf("head_block_number = %v, want 100", props["head_block_number"])
	}
	if n := bad.requests.Load(); n != 1 {
		t.Errorf("bad node got %d requests, want 1 (rotate past it)", n)
	}
}

func TestGetBlockRotatesOnMalformedResult(t *testing.T) {
	bad := newMalformedResultServer()
	defer bad.Close()
	good := newFakeRPCServer(t, 100, func(int64) map[string]any { return nil })

	c := NewHiveClient([]string{bad.URL, good.URL})

	// First call hits the bad node: the envelope parses but the result
	// payload does not, so it must fail with the malformed error and
	// rotate to the next node.
	if _, err := c.GetBlock(context.Background(), 100); err == nil {
		t.Fatal("GetBlock succeeded against malformed node, want error")
	} else if got := err.Error(); got != "malformed block response: json: cannot unmarshal number into Go value of type map[string]interface {}" {
		t.Errorf("unexpected error: %v", got)
	}

	// Second call must use the rotated-to good node.
	if _, err := c.GetBlock(context.Background(), 100); err != nil {
		t.Fatalf("GetBlock after rotation failed: %v", err)
	}
	if n := bad.requests.Load(); n != 1 {
		t.Errorf("bad node got %d requests, want 1 (rotated away)", n)
	}
}

func TestRPCCallAllNodesEmptyResult(t *testing.T) {
	bad1 := newEmptyResultServer()
	defer bad1.Close()
	bad2 := newEmptyResultServer()
	defer bad2.Close()

	c := NewHiveClient([]string{bad1.URL, bad2.URL})
	_, err := c.RPCCall(context.Background(), "condenser_api.get_block", []any{100})
	if err == nil {
		t.Fatal("RPCCall succeeded with all nodes returning empty results")
	}
	if !strings.Contains(err.Error(), "all nodes failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWatcherRecoversFromEmptyResults(t *testing.T) {
	// Mirrors the production log: a node answering without a result field
	// must not stall the watcher; it should rotate to the healthy node.
	bad := newEmptyResultServer()
	defer bad.Close()

	opJSON := `{"version":"1.1","medium":"podcast","reason":"update","iris":["https://example.com/feed.xml"]}`
	good := newFakeRPCServer(t, 1000, func(n int64) map[string]any {
		if n == 1000 {
			return podpingBlock(1000, "trx1", "pp_podcast_update", opJSON)
		}
		return nil
	})

	w := NewWatcher([]string{bad.URL, good.URL})
	w.running = true

	var got []PodpingData
	var mu sync.Mutex
	w.OnUpdate(func(d PodpingData) {
		mu.Lock()
		got = append(got, d)
		mu.Unlock()
	})

	w.processBlocks(context.Background(), NewHiveClient([]string{bad.URL, good.URL}))

	mu.Lock()
	defer mu.Unlock()
	if cb, ok := w.CurrentBlock(); !ok || cb != 1001 {
		t.Fatalf("current block = %v/%v, want 1001", cb, ok)
	}
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
}
