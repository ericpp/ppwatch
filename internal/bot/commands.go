package bot

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ericpp/ppwatch/internal/irc"
)

// const identifyTimeout is how long the bot waits for a NickServ reply.
const identifyTimeout = 30 * time.Second

// errFeedNotFound mirrors the ValueError raised by _write_podping.
type errFeedNotFound struct{ feedID int64 }

func (e *errFeedNotFound) Error() string {
	return fmt.Sprintf("Feed ID %d not found in Podcast Index", e.feedID)
}

// setupHandlers registers the IRC event handlers.
func (b *Bot) setupHandlers(c *irc.Client) {
	c.OnConnected(func(c *irc.Client) error {
		log.Printf("Connected to %s", b.Config.IrcHost)

		if c.Nick() != b.Config.IrcNick {
			log.Printf("Registered nick %s is still taken (connected as %s); reconnecting",
				b.Config.IrcNick, c.Nick())
			return fmt.Errorf("registered nick %s is still taken", b.Config.IrcNick)
		}

		if b.Config.IrcNickservPassword != "" {
			log.Printf("Identifying with NickServ...")
			re := regexp.MustCompile(`Password accepted|identified`)
			ch, cancel := c.AwaitMessage("NickServ", re, identifyTimeout)
			if err := c.Message("NickServ", "IDENTIFY "+b.Config.IrcNickservPassword); err != nil {
				cancel()
				return err
			}
			select {
			case <-ch:
				log.Printf("NickServ identify accepted")
			case <-time.After(identifyTimeout):
				log.Printf("NickServ identify timed out")
				cancel()
				return fmt.Errorf("NickServ identify timed out")
			}
			cancel()
		}

		for channel := range b.Config.ChannelSubscriptions {
			log.Printf("Joining %s", channel)
			if err := c.Join(channel); err != nil {
				return err
			}
			b.mu.Lock()
			b.joined[channel] = true
			b.mu.Unlock()
		}
		return nil
	})

	// Channel commands: !pp, !pplive, !ppend
	c.OnMessage(func(m *irc.Message) bool {
		return m.ToChannel() && (strings.HasPrefix(m.Text, "!pp") ||
			strings.HasPrefix(m.Text, "!pplive") ||
			strings.HasPrefix(m.Text, "!ppend"))
	}, func(c *irc.Client, m *irc.Message) {
		// Parse command (remove ! prefix for channel commands)
		text := strings.TrimPrefix(m.Text, "!")
		parts := strings.Fields(text)
		if len(parts) == 0 || m.Nick == "" {
			return
		}
		channel := m.Recipient()
		cmd := parts[0]
		if cmd == "pp" || cmd == "pplive" || cmd == "ppend" {
			b.RouteCommand(channel, m.Nick, parts, channel)
		}
	})

	// Private message commands (no ! prefix)
	c.OnMessage(func(m *irc.Message) bool {
		return !m.ToChannel()
	}, func(c *irc.Client, m *irc.Message) {
		parts := strings.Fields(m.Text)
		if len(parts) == 0 || m.Nick == "" {
			return
		}
		b.RouteCommand(m.Nick, m.Nick, parts, "")
	})
}

// RouteCommand routes a ppwatch command to the appropriate handler.
func (b *Bot) RouteCommand(target, nick string, parts []string, channel string) {
	if len(parts) == 0 || parts[0] == "help" {
		b.HandleHelp(nick)
		return
	}

	switch parts[0] {
	case "list":
		b.HandleList(nick, channel)

	case "pp":
		if len(parts) < 2 {
			b.SendMessage(target, "Usage: !pp <feed_id_or_alias>")
		} else {
			b.HandlePP(nick, parts[1], "update", target)
		}

	case "pplive":
		if len(parts) < 2 {
			b.SendMessage(target, "Usage: !pplive <feed_id_or_alias>")
		} else {
			b.HandlePP(nick, parts[1], "live", target)
		}

	case "ppend":
		if len(parts) < 2 {
			b.SendMessage(target, "Usage: !ppend <feed_id_or_alias>")
		} else {
			b.HandlePP(nick, parts[1], "liveEnd", target)
		}
	}
}

// HandleHelp sends the help message.
func (b *Bot) HandleHelp(nick string) {
	cmd := b.Config.CommandName
	b.SendMessage(nick, fmt.Sprintf("=== %s Bot Commands ===", strings.ToUpper(cmd)))
	b.SendMessage(nick, "  help - Show this help")
	b.SendMessage(nick, "  list - Show all subscriptions")
	b.SendMessage(nick, "  pp <feed_id_or_alias> - Send a podping notification for an updated feed")
	b.SendMessage(nick, "  pplive <feed_id_or_alias> - Send a podping notification for a live feed")
	b.SendMessage(nick, "  ppend <feed_id_or_alias> - Send a podping notification for an ended live feed")
	if len(b.Config.FeedAliases) > 0 {
		names := make([]string, 0, len(b.Config.FeedAliases))
		for name := range b.Config.FeedAliases {
			names = append(names, name)
		}
		sort.Strings(names)
		pairs := make([]string, 0, len(names))
		for _, name := range names {
			pairs = append(pairs, fmt.Sprintf("%s=%d", name, b.Config.FeedAliases[name]))
		}
		b.SendMessage(nick, "    Aliases: "+strings.Join(pairs, ", "))
	}
}

// HandleList lists subscriptions (specific channel or all).
func (b *Bot) HandleList(nick string, channel string) {
	if channel != "" {
		subs, ok := b.Config.ChannelSubscriptions[channel]
		if !ok || len(subs) == 0 {
			b.SendMessage(nick, fmt.Sprintf("No subscriptions for %s", channel))
			return
		}
		b.SendMessage(nick, fmt.Sprintf("Monitoring %d feed(s) for %s:", len(subs), channel))
		for _, url := range sortedURLs(subs) {
			b.SendMessage(nick, "  "+url)
		}
		return
	}

	if len(b.Config.ChannelSubscriptions) == 0 {
		b.SendMessage(nick, "No subscriptions configured")
		return
	}
	b.SendMessage(nick, fmt.Sprintf("Subscriptions (%d channels):", len(b.Config.ChannelSubscriptions)))
	for ch, subs := range b.Config.ChannelSubscriptions {
		b.SendMessage(nick, fmt.Sprintf("  %s: %d feed(s)", ch, len(subs)))
		for _, url := range sortedURLs(subs) {
			b.SendMessage(nick, "    "+url)
		}
	}
}

func sortedURLs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}

// ResolveFeedAlias resolves a feed alias to its numeric ID.
// Returns (resolvedID, aliasNameOrEmpty).
func (b *Bot) ResolveFeedAlias(feedIDStr string) (string, string) {
	aliasKey := strings.ToLower(feedIDStr)
	if id, ok := b.Config.FeedAliases[aliasKey]; ok {
		return strconv.FormatInt(id, 10), feedIDStr
	}
	return feedIDStr, ""
}
