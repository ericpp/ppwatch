package hive

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"time"
)

// PodpingWriter sends podcast update notifications to the Hive blockchain.
type PodpingWriter struct {
	account    string
	postingKey string
	dryRun     bool
	sessionID  uint64
	client     *HiveClient
}

// NewPodpingWriter creates a writer.
func NewPodpingWriter(account, postingKey string, nodes []string, dryRun bool) (*PodpingWriter, error) {
	if _, err := wifToSecret(postingKey); err != nil {
		return nil, err
	}
	var sid [8]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return nil, err
	}
	return &PodpingWriter{
		account:    account,
		postingKey: postingKey,
		dryRun:     dryRun,
		sessionID:  binary.BigEndian.Uint64(sid[:]),
		client:     NewHiveClient(nodes),
	}, nil
}

// Account returns the writer's Hive account name.
func (w *PodpingWriter) Account() string { return w.account }

// Post posts an update notification for one or more feed URLs.
// It returns the transaction id.
func (w *PodpingWriter) Post(ctx context.Context, urls []string, reason, medium string) (string, error) {
	if medium == "" {
		medium = "podcast"
	}
	if reason == "" {
		reason = "update"
	}

	for _, u := range urls {
		if !validIRI(u) {
			return "", NewValidationError("Invalid URL: %s", u)
		}
	}

	if w.dryRun {
		log.Printf("DRY RUN - Would post notification for %d URLs", len(urls))
		return "dry_run", nil
	}

	payload := map[string]any{
		"version":     "1.1",
		"medium":      medium,
		"reason":      reason,
		"iris":        urls,
		"timestampNs": time.Now().UTC().UnixNano(),
		"sessionId":   w.sessionID,
	}
	jsonStr, err := json.Marshal(payload)
	if err != nil {
		return "", NewError("marshal payload: %v", err)
	}
	if len(jsonStr) > 8192 {
		return "", NewValidationError("Too many URLs (payload exceeds 8KB limit)")
	}

	operations := [][]any{
		{"custom_json", map[string]any{
			"required_auths":        []string{},
			"required_posting_auths": []string{w.account},
			"id":                    fmt.Sprintf("pp_%s_%s", medium, reason),
			"json":                  string(jsonStr),
		}},
	}

	builder := NewTransactionBuilder(w.client)
	txID, err := builder.Broadcast(ctx, operations, w.postingKey, false)
	if err != nil {
		return "", err
	}
	log.Printf("Posted notification for %d URLs: %s", len(urls), txID)
	return txID, nil
}

// GetCredits returns the remaining Resource Credits as a percentage (0-100).
func (w *PodpingWriter) GetCredits(ctx context.Context) (float64, error) {
	rc, err := GetAccountRC(ctx, w.client, w.account)
	if err != nil {
		return 0, err
	}
	return rc, nil
}

// validIRI performs a pragmatic IRI check: spaces are encoded and the result
// must parse as an absolute URL.
func validIRI(raw string) bool {
	normalized := ""
	for _, r := range raw {
		if r == ' ' {
			normalized += "%20"
		} else {
			normalized += string(r)
		}
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return false
	}
	if u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.Host == "" && u.Opaque == "" {
		return false
	}
	return true
}
