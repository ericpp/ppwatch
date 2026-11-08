package bot

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericpp/ppwatch/internal/config"
)

// botTestServer is a scripted IRC server for bot-level tests.
type botTestServer struct {
	listener net.Listener
	received chan string

	mu   sync.Mutex
	conn net.Conn
}

func newBotTestServer(t *testing.T) *botTestServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &botTestServer{listener: ln, received: make(chan string, 64)}
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *botTestServer) serve(t *testing.T, respond func(string) []string) {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	go func() {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case s.received <- line:
			default:
			}
			for _, reply := range respond(line) {
				conn.Write([]byte(reply + "\r\n"))
			}
		}
	}()
}

func (s *botTestServer) send(t *testing.T, line string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		t.Fatal("no client connection")
	}
	s.conn.Write([]byte(line + "\r\n"))
}

func (s *botTestServer) expectLine(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line := <-s.received:
			if line == want {
				return
			}
			// ignore unrelated lines (registration chatter etc.)
		case <-deadline:
			t.Fatalf("did not receive %q in time", want)
		}
	}
}

func (s *botTestServer) port() int { return s.listener.Addr().(*net.TCPAddr).Port }

func welcomeIRC(nick string) []string {
	return []string{
		":irc 001 " + nick + " :Welcome to the Test Network",
		":irc 376 " + nick + " :End of /MOTD command.",
	}
}

// startBot connects a bot to the scripted server.
func startBot(t *testing.T, cfg *config.BotConfig, srv *botTestServer) *Bot {
	t.Helper()
	cfg.IrcHost = "127.0.0.1"
	cfg.IrcPort = srv.port()
	cfg.IrcSecure = false

	b := New(cfg)
	c := b.createIRCClient()
	b.mu.Lock()
	b.irc = c
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = c.Run(ctx) }()

	return b
}

func TestCommandRouting(t *testing.T) {
	cfg := &config.BotConfig{
		IrcNick:     "ppwatch",
		CommandName: "ppwatch",
		FeedAliases: map[string]int64{"bts": 150842},
		ChannelSubscriptions: map[string]map[string]bool{
			"#test": {"https://example.com/feed.xml": true},
		},
	}
	srv := newBotTestServer(t)
	joined := make(chan bool, 1)
	go srv.serve(t, func(line string) []string {
		if strings.HasPrefix(line, "NICK ") {
			return welcomeIRC(strings.TrimPrefix(line, "NICK "))
		}
		if strings.HasPrefix(line, "JOIN ") {
			joined <- true
			return []string{":ppwatch!ppwatch@host JOIN :#test"}
		}
		return nil
	})

	startBot(t, cfg, srv)

	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("bot did not join")
	}

	// Private message: "help" (no ! prefix)
	srv.send(t, ":alice!a@host PRIVMSG ppwatch :help")
	srv.expectLine(t, "PRIVMSG alice :=== PPWATCH Bot Commands ===", 3*time.Second)
	srv.expectLine(t, "PRIVMSG alice :  help - Show this help", 3*time.Second)

	// Private message: list
	srv.send(t, ":alice!a@host PRIVMSG ppwatch :list")
	srv.expectLine(t, "PRIVMSG alice :Subscriptions (1 channels):", 3*time.Second)
	srv.expectLine(t, "PRIVMSG alice :  #test: 1 feed(s)", 3*time.Second)

	// Channel command: !pp with alias
	srv.send(t, ":alice!a@host PRIVMSG #test :!pp bts")
	srv.expectLine(t, "PRIVMSG #test :Sending podping for bts (feed ID 150842)...", 3*time.Second)
	srv.expectLine(t, "PRIVMSG #test :Error: Podcast Index not configured", 3*time.Second)

	// Channel command with invalid feed id: the Podcast Index check comes
	// first, before feed id validation.
	srv.send(t, ":alice!a@host PRIVMSG #test :!pp abc")
	srv.expectLine(t, "PRIVMSG #test :Sending podping for feed ID abc...", 3*time.Second)
	srv.expectLine(t, "PRIVMSG #test :Error: Podcast Index not configured", 3*time.Second)
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"HTTP://Example.COM/Feed/":  "https://example.com/feed",
		"https://example.com/a.xml": "https://example.com/a.xml",
		"http://x.y/z/":             "https://x.y/z",
	}
	for in, want := range cases {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveFeedAlias(t *testing.T) {
	b := New(&config.BotConfig{FeedAliases: map[string]int64{"bts": 150842}})
	id, alias := b.ResolveFeedAlias("BTS")
	if id != "150842" || alias != "BTS" {
		t.Errorf("ResolveFeedAlias(BTS) = %q, %q", id, alias)
	}
	id, alias = b.ResolveFeedAlias("999")
	if id != "999" || alias != "" {
		t.Errorf("ResolveFeedAlias(999) = %q, %q", id, alias)
	}
}
