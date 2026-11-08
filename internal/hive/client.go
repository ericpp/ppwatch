package hive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// HiveNodes is the default list of Hive JSON-RPC nodes.
var HiveNodes = []string{
	"https://api.hive.blog",
	"https://api.openhive.network",
	"https://anyx.io",
	"https://rpc.ausbit.dev",
	"https://rpc.mahdiyari.info",
	"https://api.hive.blue",
	"https://techcoderx.com",
	"https://hive.roelandp.nl",
	"https://hived.emre.sh",
	"https://api.deathwing.me",
	"https://api.c0ff33a.uk",
	"https://hive-api.arcange.eu",
	"https://hive-api.3speak.tv",
	"https://hiveapi.actifit.io",
}

// rpcError mirrors the JSON-RPC error object.
type rpcError struct {
	Code    any             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
	ID      int             `json:"id"`
}

// HiveClient is a Hive JSON-RPC client with automatic node failover.
type HiveClient struct {
	nodes []string

	mu      sync.Mutex
	nodeIdx int

	http *http.Client
}

// NewHiveClient creates a client; nodes falling back to HiveNodes when empty.
func NewHiveClient(nodes []string) *HiveClient {
	if len(nodes) == 0 {
		nodes = append([]string(nil), HiveNodes...)
	}
	return &HiveClient{
		nodes: nodes,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

// Nodes returns the configured node list.
func (c *HiveClient) Nodes() []string { return c.nodes }

func (c *HiveClient) nextNode() {
	c.mu.Lock()
	c.nodeIdx = (c.nodeIdx + 1) % len(c.nodes)
	c.mu.Unlock()
}

func (c *HiveClient) currentNode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nodes[c.nodeIdx]
}

// formatRPCError builds a readable message from an RPC error object,
// including code and nested error details when present.
func formatRPCError(e *rpcError) string {
	if e == nil {
		return "Unknown RPC error"
	}
	msg := e.Message
	if msg == "" {
		msg = "Unknown RPC error"
	}
	if e.Code != nil {
		msg = fmt.Sprintf("%s (code %v)", msg, e.Code)
	}
	if len(e.Data) > 0 && string(e.Data) != "null" {
		var dataMap map[string]any
		if err := json.Unmarshal(e.Data, &dataMap); err == nil {
			name, _ := dataMap["name"].(string)
			detailMsg, _ := dataMap["message"].(string)
			var parts []string
			if name != "" {
				parts = append(parts, name)
			}
			if detailMsg != "" {
				parts = append(parts, detailMsg)
			}
			detail := ""
			for i, p := range parts {
				if i > 0 {
					detail += ": "
				}
				detail += p
			}
			if detail != "" && detail != msg {
				msg = fmt.Sprintf("%s: %s", msg, detail)
			}
		} else {
			msg = fmt.Sprintf("%s: %s", msg, string(e.Data))
		}
	}
	return msg
}

// RPCCall performs a JSON-RPC call, rotating to the next node on transport
// failures until every node has been tried once.
func (c *HiveClient) RPCCall(ctx context.Context, method string, params []any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return nil, NewError("marshal request: %v", err)
	}

	var lastErr error
	for i := 0; i < len(c.nodes); i++ {
		node := c.currentNode()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, node, bytes.NewReader(payload))
		if err != nil {
			return nil, NewError("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Node %s failed: %v", node, err)
			c.nextNode()
			select {
			case <-ctx.Done():
				return nil, NewConnectionError("all nodes failed. Last error: %v", ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			log.Printf("Node %s failed: %v", node, err)
			c.nextNode()
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var r rpcResponse
		if err := json.Unmarshal(body, &r); err != nil {
			lastErr = err
			log.Printf("Node %s failed: %v", node, err)
			c.nextNode()
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if r.Error != nil {
			// RPC-level error: rotate and surface as a network error.
			c.nextNode()
			return nil, NewNetworkError("%s", formatRPCError(r.Error))
		}

		return r.Result, nil
	}

	return nil, NewConnectionError("all nodes failed. Last error: %v", lastErr)
}

// GetDynamicGlobalProperties returns the dynamic global properties object.
func (c *HiveClient) GetDynamicGlobalProperties(ctx context.Context) (map[string]any, error) {
	result, err := c.RPCCall(ctx, "condenser_api.get_dynamic_global_properties", nil)
	if err != nil {
		return nil, err
	}
	var props map[string]any
	if err := json.Unmarshal(result, &props); err != nil {
		return nil, NewError("malformed dynamic global properties response: %v", err)
	}
	return props, nil
}

// GetBlock returns the block with the given number, or nil when missing.
func (c *HiveClient) GetBlock(ctx context.Context, blockNum int64) (map[string]any, error) {
	result, err := c.RPCCall(ctx, "condenser_api.get_block", []any{blockNum})
	if err != nil {
		return nil, err
	}
	if string(result) == "null" {
		return nil, nil
	}
	var block map[string]any
	if err := json.Unmarshal(result, &block); err != nil {
		return nil, NewError("malformed block response: %v", err)
	}
	return block, nil
}

// GetBlockHeader returns the block header with the given number.
func (c *HiveClient) GetBlockHeader(ctx context.Context, blockNum int64) (map[string]any, error) {
	result, err := c.RPCCall(ctx, "condenser_api.get_block_header", []any{blockNum})
	if err != nil {
		return nil, err
	}
	if string(result) == "null" {
		return nil, nil
	}
	var header map[string]any
	if err := json.Unmarshal(result, &header); err != nil {
		return nil, NewError("malformed block header response: %v", err)
	}
	return header, nil
}

