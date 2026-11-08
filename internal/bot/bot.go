// Package bot implements the podping IRC bot.
package bot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ericpp/ppwatch/internal/config"
	"github.com/ericpp/ppwatch/internal/hive"
	"github.com/ericpp/ppwatch/internal/irc"
	"github.com/ericpp/ppwatch/internal/podcastindex"
)

// VALIDPPReasons is the set of accepted podping reasons.
var VALIDPPReasons = map[string]bool{"live": true, "liveEnd": true, "update": true}

// errTimeout signals a command timeout, mirroring asyncio.TimeoutError.
var errTimeout = fmt.Errorf("timeout")

// Bot is the IRC bot that monitors podping and posts updates to channels.
type Bot struct {
	Config *config.BotConfig

	normalized map[string]map[string]bool

	mu      sync.Mutex
	joined  map[string]bool
	http    *http.Client
	irc     *irc.Client
	podcast *podcastindex.Client
	writer  *hive.PodpingWriter
	watcher *hive.Watcher
}

// New creates a bot from the configuration.
func New(cfg *config.BotConfig) *Bot {
	return &Bot{
		Config:     cfg,
		normalized: NormalizeSubscriptions(cfg.ChannelSubscriptions),
		joined:     map[string]bool{},
	}
}

// NormalizeSubscriptions precomputes normalized URL sets per channel.
func NormalizeSubscriptions(subs map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(subs))
	for ch, urls := range subs {
		set := make(map[string]bool, len(urls))
		for u := range urls {
			set[NormalizeURL(u)] = true
		}
		out[ch] = set
	}
	return out
}

// NormalizeURL normalizes a URL for comparison (https, lowercase, no
// trailing slash).
func NormalizeURL(url string) string {
	u := strings.ToLower(url)
	u = strings.TrimRight(u, "/")
	if strings.HasPrefix(u, "http://") {
		u = "https://" + u[7:]
	}
	return u
}

// Run runs the bot with automatic reconnection, until the context is done.
func (b *Bot) Run(ctx context.Context) error {
	const reconnectDelay = 30 * time.Second

	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		b.startWatcher(ctx)
	}()
	defer func() { <-watcherDone }()

	for {
		c := b.createIRCClient()
		b.mu.Lock()
		b.irc = c
		b.mu.Unlock()

		err := c.Run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			log.Printf("IRC connection closed")
		} else {
			log.Printf("IRC connection error: %v", err)
		}

		b.mu.Lock()
		b.joined = map[string]bool{}
		b.mu.Unlock()

		log.Printf("Reconnecting in %v seconds...", reconnectDelay.Seconds())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

func (b *Bot) createIRCClient() *irc.Client {
	c := irc.NewClient(irc.Config{
		Host:        b.Config.IrcHost,
		Port:        b.Config.IrcPort,
		Secure:      b.Config.IrcSecure,
		Nick:        b.Config.IrcNick,
		User:        b.Config.IrcUser,
		Realname:    b.Config.IrcRealname,
		ReadTimeout: 300 * time.Second,
	})
	b.setupHandlers(c)
	return c
}

// startWatcher initializes Podcast Index / podping writer and runs the
// Hive watcher loop until the context is done.
func (b *Bot) startWatcher(ctx context.Context) {
	if b.Config.PodcastIndexKey != "" && b.Config.PodcastIndexSecret != "" {
		b.podcast = podcastindex.NewClient(b.Config.PodcastIndexKey, b.Config.PodcastIndexSecret, "")
		log.Printf("Podcast Index client initialized")
	} else {
		log.Printf("Podcast Index not configured - metadata unavailable")
	}

	if b.Config.HiveAccount != "" && b.Config.HivePostingKey != "" {
		w, err := hive.NewPodpingWriter(
			b.Config.HiveAccount,
			b.Config.HivePostingKey,
			b.Config.HiveNodes,
			b.Config.HiveDryRun,
		)
		if err != nil {
			log.Printf("Error initializing podping writer: %v", err)
		} else {
			b.writer = w
			log.Printf("Podping writer initialized (dry_run=%v)", b.Config.HiveDryRun)
		}
	} else {
		log.Printf("Podping writer not configured - !pp command unavailable")
	}

	b.watcher = hive.NewWatcher(nil)
	b.watcher.OnUpdate(b.HandlePodping)

	log.Printf("Starting podping watcher...")
	if err := b.watcher.Start(ctx); err != nil {
		log.Printf("Podping watcher stopped: %v", err)
	}
}

// SendMessage sends a message with error handling.
func (b *Bot) SendMessage(target, text string) {
	b.mu.Lock()
	c := b.irc
	b.mu.Unlock()
	if c == nil {
		return
	}
	if err := c.Message(target, text); err != nil {
		log.Printf("Failed to send message to %s: %v", target, err)
	}
}

// InChannel reports whether the bot believes it has joined the channel.
func (b *Bot) InChannel(channel string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.joined[channel]
}
