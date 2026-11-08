package podcastindex

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIError is a Podcast Index API error.
type APIError struct{ Msg string }

func (e *APIError) Error() string { return e.Msg }

// AuthError is raised when authentication fails.
type AuthError struct{ APIError }

func newError(format string, args ...any) *APIError {
	return &APIError{Msg: fmt.Sprintf(format, args...)}
}

// Metadata holds podcast metadata from the Podcast Index API.
type Metadata struct {
	ID             int64             `json:"id"`
	Title          string            `json:"title"`
	URL            string            `json:"url"`
	OriginalURL    string            `json:"originalUrl"`
	Link           string            `json:"link"`
	Description    string            `json:"description"`
	Author         string            `json:"author"`
	Image          string            `json:"image"`
	LastUpdateTime int64             `json:"lastUpdateTime"`
	LastCrawlTime  int64             `json:"lastCrawlTime"`
	ItunesID       int64             `json:"itunesId"`
	Language       string            `json:"language"`
	Categories     map[string]string `json:"categories"`
}

// DisplayName returns the title, or the URL when no title is set.
func (m *Metadata) DisplayName() string {
	if m.Title != "" {
		return m.Title
	}
	return m.URL
}

// Client is a client for the Podcast Index API.
type Client struct {
	APIKey    string
	APISecret string
	BaseURL   string

	http *http.Client
}

// NewClient creates a Podcast Index API client.
func NewClient(apiKey, apiSecret, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.podcastindex.org/api/1.0"
	}
	return &Client{
		APIKey:    apiKey,
		APISecret: apiSecret,
		BaseURL:   baseURL,
		http:      &http.Client{},
	}
}

func (c *Client) authHeaders() map[string]string {
	unixTime := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha1.Sum([]byte(c.APIKey + c.APISecret + unixTime))
	return map[string]string{
		"User-Agent":    "ppwatch/1.0.0",
		"X-Auth-Date":   unixTime,
		"X-Auth-Key":    c.APIKey,
		"Authorization": hex.EncodeToString(sum[:]),
	}
}

// lookup performs an authenticated GET and parses the feed from the response.
// It returns (nil, nil) when the feed is not found.
func (c *Client) lookup(ctx context.Context, endpoint string, params url.Values) (*Metadata, error) {
	u := c.BaseURL + endpoint + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, newError("build request: %v", err)
	}
	for k, v := range c.authHeaders() {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, newError("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &AuthError{APIError{"Authentication failed - check API credentials"}}
	}
	if resp.StatusCode != http.StatusOK {
		reason := ""
		if resp.Status != "" && len(resp.Status) > 4 {
			reason = resp.Status[4:]
		}
		return nil, newError("HTTP %d: %s", resp.StatusCode, reason)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, newError("Request failed: %v", err)
	}

	var data struct {
		Status      string          `json:"status"`
		Description string          `json:"description"`
		Feed        json.RawMessage `json:"feed"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, newError("Unexpected error: %v", err)
	}

	if data.Status == "false" {
		desc := data.Description
		if strings.Contains(strings.ToLower(desc), "not found") {
			return nil, nil
		}
		return nil, newError("%s", desc)
	}

	if len(data.Feed) == 0 || string(data.Feed) == "null" {
		return nil, nil
	}

	var m Metadata
	if err := json.Unmarshal(data.Feed, &m); err != nil {
		return nil, newError("Unexpected error: %v", err)
	}
	return &m, nil
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// LookupByFeedURL looks up podcast information by feed URL.
// Returns nil metadata when the feed is not found.
func (c *Client) LookupByFeedURL(ctx context.Context, feedURL string) (*Metadata, error) {
	params := url.Values{}
	params.Set("url", feedURL)
	return c.lookup(ctx, "/podcasts/byfeedurl", params)
}

// LookupByFeedID looks up podcast information by feed ID.
// Returns nil metadata when the feed is not found.
func (c *Client) LookupByFeedID(ctx context.Context, feedID int64) (*Metadata, error) {
	params := url.Values{}
	params.Set("id", strconv.FormatInt(feedID, 10))
	return c.lookup(ctx, "/podcasts/byfeedid", params)
}
