package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ericpp/ppwatch/internal/hive"
	"github.com/ericpp/ppwatch/internal/rss"
)

// httpClient lazily creates the shared HTTP client.
func (b *Bot) httpClient() *http.Client {
	if b.http == nil {
		b.http = &http.Client{}
	}
	return b.http
}

// GetPodcastInfo gets the podcast title (and unused ID) from Podcast Index
// with the configured timeout. Returns empty strings on failure.
func (b *Bot) GetPodcastInfo(url string) (string, int64) {
	if b.podcast == nil {
		return "", 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.Config.APITimeout*float64(time.Second)))
	defer cancel()

	metadata, err := b.podcast.LookupByFeedURL(ctx, url)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("Timeout fetching metadata for %s", url)
		} else {
			log.Printf("Failed to fetch metadata for %s: %v", url, err)
		}
		return "", 0
	}
	if metadata == nil {
		return "", 0
	}
	return metadata.Title, metadata.ID
}

// CheckLiveItemStatus fetches the feed and checks whether the liveItem
// closest to now has status="live". Returns (isLive, errorMessage) where
// isLive < 0 means "could not determine".
func (b *Bot) CheckLiveItemStatus(feedURL string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.Config.APITimeout*float64(time.Second)))
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		msg := fmt.Sprintf("Request error fetching feed %s: %v", feedURL, err)
		log.Printf("%s", msg)
		return -1, msg
	}
	resp, err := b.httpClient().Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			msg := fmt.Sprintf("Timeout fetching feed %s", feedURL)
			log.Printf("%s", msg)
			return -1, msg
		}
		msg := fmt.Sprintf("Request error fetching feed %s: %v", feedURL, err)
		log.Printf("%s", msg)
		return -1, msg
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("HTTP %d %s fetching feed %s", resp.StatusCode, http.StatusText(resp.StatusCode), feedURL)
		log.Printf("%s", msg)
		return -1, msg
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		msg := fmt.Sprintf("Request error fetching feed %s: %v", feedURL, err)
		log.Printf("%s", msg)
		return -1, msg
	}

	liveItems := rss.FindLiveItems(body)
	if len(liveItems) == 0 {
		log.Printf("No liveItem elements found in %s", feedURL)
		return 0, ""
	}

	closest, ok := rss.FindClosestLiveItem(liveItems)
	if !ok {
		log.Printf("No liveItem elements found in %s", feedURL)
		return 0, ""
	}

	live := 0
	if closest.IsLive() {
		live = 1
	}
	log.Printf("Closest liveItem in %s has status='%s' (start=%s)", feedURL, closest.Status, closest.Start)
	return live, ""
}

// VerifyLiveStatus verifies that the feed's liveItem status matches the
// podping reason. Returns (isValid, message).
func (b *Bot) VerifyLiveStatus(url, reason string) (bool, string) {
	if reason != "live" && reason != "liveEnd" {
		return true, ""
	}

	time.Sleep(5 * time.Second)
	live, errMsg := b.CheckLiveItemStatus(url)

	if live < 0 {
		warning := "Warning: Could not verify liveItem status"
		if errMsg != "" {
			warning += " - " + errMsg
		}
		log.Printf("%s for %s (reason: %s)", warning, url, reason)
		return true, warning
	}

	if reason == "live" && live == 0 {
		errMsg := "Error: Feed has no liveItem with status='live' but reason is 'live'"
		log.Printf("%s for %s", errMsg, url)
		return false, errMsg
	}

	if reason == "liveEnd" && live == 1 {
		errMsg := "Error: Feed has liveItem with status='live' but reason is 'liveEnd'"
		log.Printf("%s for %s", errMsg, url)
		return false, errMsg
	}

	return true, ""
}

// HandlePodping handles incoming podping data.
func (b *Bot) HandlePodping(data hive.PodpingData) {
	channelURLs := map[string][]string{}
	for _, url := range data.URLs {
		normalized := NormalizeURL(url)
		for channel, urls := range b.normalized {
			if urls[normalized] {
				channelURLs[channel] = append(channelURLs[channel], url)
			}
		}
	}

	if len(channelURLs) == 0 {
		log.Printf("Unrelated podping with %d URL(s) %s", len(data.URLs), strings.Join(data.URLs, "; "))
		return
	}

	log.Printf("Received podping with %d URL(s): %s", len(data.URLs), strings.Join(data.URLs, "; "))

	for channel, urls := range channelURLs {
		if !b.InChannel(channel) {
			log.Printf("Not in channel %s, skipping notifications", channel)
			continue
		}

		for _, url := range urls {
			msg := b.FormatPodpingMessage(url, data.Reason, data.TrxID)
			b.SendMessage(channel, msg)
			time.Sleep(time.Duration(b.Config.MessageDelay * float64(time.Second)))

			if data.Reason == "live" || data.Reason == "liveEnd" {
				valid, message := b.VerifyLiveStatus(url, data.Reason)
				if !valid || message != "" {
					b.SendMessage(channel, "  → "+message)
					time.Sleep(time.Duration(b.Config.MessageDelay * float64(time.Second)))
				}
			}
		}
	}
}

// FormatPodpingMessage formats a podping notification message.
func (b *Bot) FormatPodpingMessage(url, reason, trxID string) string {
	title, _ := b.GetPodcastInfo(url)
	podcastName := title
	if podcastName == "" {
		podcastName = "Unknown Podcast"
	}
	return fmt.Sprintf("Podping received: %s %s (%s) (tx: %s)", podcastName, url, reason, trxID)
}
