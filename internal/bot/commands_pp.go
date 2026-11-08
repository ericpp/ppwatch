package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/ericpp/ppwatch/internal/hive"
)

// HandlePP handles the !pp / !pplive / !ppend commands.
func (b *Bot) HandlePP(nick, feedIDStr, reason string, channel string) {
	target := channel
	if target == "" {
		target = nick
	}

	resolved, aliasUsed := b.ResolveFeedAlias(feedIDStr)
	if aliasUsed != "" {
		b.SendMessage(target, fmt.Sprintf("Sending podping for %s (feed ID %s)...", aliasUsed, resolved))
	} else {
		b.SendMessage(target, fmt.Sprintf("Sending podping for feed ID %s...", resolved))
	}

	if b.podcast == nil {
		b.SendMessage(target, "Error: Podcast Index not configured")
		return
	}
	if b.writer == nil {
		b.SendMessage(target, "Error: Podping writer not configured")
		return
	}

	feedID, err := strconv.ParseInt(resolved, 10, 64)
	if err != nil {
		b.SendMessage(target, fmt.Sprintf("Error: Invalid feed ID '%s' (must be a number or alias)", resolved))
		return
	}

	if reason != "" && !VALIDPPReasons[reason] {
		b.SendMessage(target, fmt.Sprintf("Error: Invalid reason '%s'. Valid reasons are: %s", reason, sortedReasons()))
		return
	}

	effectiveReason := reason
	if effectiveReason == "" {
		effectiveReason = "update"
	}

	msg, feedURL, reasonUsed, err := b.WritePodping(feedID, effectiveReason, nick)
	if err != nil {
		var nfErr *errFeedNotFound
		switch {
		case errors.Is(err, errTimeout):
			log.Printf("Timeout in pp command by %s for feed %d", nick, feedID)
			b.SendMessage(target, fmt.Sprintf("Error: Timeout writing podping for feed %d (try again later)", feedID))
		case errors.As(err, &nfErr):
			b.SendMessage(target, fmt.Sprintf("Error: %v", err))
		case isNetworkErr(err):
			log.Printf("Network error in pp command by %s for feed %d: %v", nick, feedID, err)
			b.SendMessage(target, fmt.Sprintf("Error: Hive network error for feed %d - please try again in a moment", feedID))
		case isHiveErr(err):
			log.Printf("Podping error in pp command by %s for feed %d: %v", nick, feedID, err)
			b.SendMessage(target, fmt.Sprintf("Error: Failed to write podping for feed %d: %v", feedID, err))
		default:
			log.Printf("Unexpected error in pp command by %s: %v", nick, err)
			b.SendMessage(target, fmt.Sprintf("Error: Unexpected error writing podping for feed %d: %v", feedID, err))
		}
		return
	}

	b.SendMessage(target, msg)

	if reasonUsed == "live" || reasonUsed == "liveEnd" {
		valid, message := b.VerifyLiveStatus(feedURL, reasonUsed)
		if !valid || message != "" {
			b.SendMessage(target, "  → "+message)
		}
	}
}

func sortedReasons() string {
	return "live, liveEnd, update"
}

func isNetworkErr(err error) bool {
	var ne *hive.PodpingNetworkError
	return errors.As(err, &ne)
}

func isHiveErr(err error) bool {
	var pe *hive.PodpingError
	return errors.As(err, &pe)
}

// WritePodping looks up the feed and writes a podping to Hive.
// Returns (resultMessage, feedURL, reason, error).
func (b *Bot) WritePodping(feedID int64, reason, nick string) (string, string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(b.Config.APITimeout*float64(time.Second)))
	defer cancel()

	metadata, err := b.podcast.LookupByFeedID(ctx, feedID)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", "", "", errTimeout
		}
		return "", "", "", err
	}

	if metadata == nil {
		return "", "", "", &errFeedNotFound{feedID: feedID}
	}

	log.Printf("Writing podping for feed %d: %s (reason: %s)", feedID, metadata.URL, reason)

	postCtx, postCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer postCancel()

	txID, err := b.writer.Post(postCtx, []string{metadata.URL}, reason, "")
	if err != nil {
		return "", "", "", err
	}

	rcInfo := ""
	rcPercent, rcErr := b.writer.GetCredits(postCtx)
	if rcErr == nil {
		rcInfo = fmt.Sprintf(" rc used: %.1f%%", 100-rcPercent)
	}

	txURL := fmt.Sprintf("https://hive.ausbit.dev/tx/%s", txID)
	msg := fmt.Sprintf("Podping sent: %s %s (%s) (tx: %s%s)", metadata.Title, metadata.URL, reason, txURL, rcInfo)
	log.Printf("Podping sent by %s for feed %d: tx %s (reason: %s)%s", nick, feedID, txID, reason, rcInfo)

	return msg, metadata.URL, reason, nil
}
