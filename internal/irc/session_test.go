package irc

import (
	"bufio"
	"context"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIRCServer drives a minimal scripted IRC session.
type fakeIRCServer struct {
	listener net.Listener
	received chan string

	mu   sync.Mutex
	conn net.Conn
}

func newFakeIRCServer(t *testing.T) *fakeIRCServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeIRCServer{listener: ln, received: make(chan string, 64)}
	t.Cleanup(func() { ln.Close() })
	return s
}

// serve runs one scripted connection; respond maps a predicate on incoming
// lines to raw reply lines.
func (s *fakeIRCServer) serve(t *testing.T, respond func(line string) []string) {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	go func() {
		// Reader exits when the test cancels the client context, which
		// closes the client side of the connection.
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

// send pushes a raw server-side line to the connected client.
func (s *fakeIRCServer) send(t *testing.T, line string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		t.Fatal("no client connection")
	}
	if _, err := s.conn.Write([]byte(line + "\r\n")); err != nil {
		t.Fatalf("send failed: %v", err)
	}
}

func (s *fakeIRCServer) nextLine(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case line := <-s.received:
		return line
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for client line")
		return ""
	}
}

// portOf returns the listener port of the fake server.
func portOf(t *testing.T, s *fakeIRCServer) int {
	t.Helper()
	return s.listener.Addr().(*net.TCPAddr).Port
}

func defaultConfig(port int) Config {
	return Config{
		Host: "127.0.0.1", Port: port,
		Nick: "ppwatch", User: "ppwatch", Realname: "Test Bot",
		ReadTimeout:       5 * time.Second,
		KeepaliveInterval: time.Hour,
	}
}

func welcome(nick string) []string {
	return []string{
		":irc 001 " + nick + " :Welcome to the Test Network",
		":irc 376 " + nick + " :End of /MOTD command.",
	}
}

func TestNickCollisionFallbackAndJoin(t *testing.T) {
	srv := newFakeIRCServer(t)
	phase := 0
	go srv.serve(t, func(line string) []string {
		if strings.HasPrefix(line, "NICK ") {
			nick := strings.TrimPrefix(line, "NICK ")
			if phase == 0 {
				phase = 1
				return []string{":irc 433 * :Nickname is already in use."}
			}
			phase = 2
			return welcome(nick)
		}
		if strings.HasPrefix(line, "JOIN ") {
			return nil
		}
		return nil
	})

	connected := make(chan string, 1)
	client := NewClient(defaultConfig(portOf(t, srv)))
	client.OnConnected(func(c *Client) error {
		connected <- c.Nick()
		return c.Join("#test")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- client.Run(ctx) }()

	gotNick := <-connected
	if gotNick != "ppwatch_" {
		t.Fatalf("connected as %q, want ppwatch_", gotNick)
	}
	// Skip registration chatter (NICK/USER echoes are not sent back, but
	// ignore any non-JOIN lines defensively).
	var line string
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line = <-srv.received:
			if strings.HasPrefix(line, "JOIN ") {
				goto joined
			}
		case <-deadline:
			t.Fatalf("no JOIN received; last line %q", line)
		}
	}
joined:
	if line != "JOIN #test" {
		t.Fatalf("expected JOIN #test, got %q", line)
	}
	cancel()
	if err := <-errCh; err != nil && err != context.Canceled {
		t.Logf("run ended: %v", err)
	}
}

func TestNickServIdentifyFlow(t *testing.T) {
	srv := newFakeIRCServer(t)
	go srv.serve(t, func(line string) []string {
		if strings.HasPrefix(line, "NICK ") {
			return welcome(strings.TrimPrefix(line, "NICK "))
		}
		if line == "PRIVMSG NickServ :IDENTIFY hunter2" {
			return []string{":NickServ!NickServ@services NOTICE ppwatch :You are now identified for ppwatch."}
		}
		return nil
	})

	client := NewClient(defaultConfig(portOf(t, srv)))
	identified := make(chan bool, 1)
	client.OnConnected(func(c *Client) error {
		re := regexp.MustCompile(`Password accepted|identified`)
		ch, cancel := c.AwaitMessage("NickServ", re, 2*time.Second)
		defer cancel()
		if err := c.Message("NickServ", "IDENTIFY hunter2"); err != nil {
			return err
		}
		select {
		case <-ch:
			identified <- true
		case <-time.After(3 * time.Second):
			identified <- false
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	select {
	case ok := <-identified:
		if !ok {
			t.Fatal("NickServ identify was not confirmed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("test timed out")
	}
}

func TestPrivmsgDispatch(t *testing.T) {
	srv := newFakeIRCServer(t)
	go srv.serve(t, func(line string) []string {
		if strings.HasPrefix(line, "NICK ") {
			return welcome(strings.TrimPrefix(line, "NICK "))
		}
		return nil
	})

	client := NewClient(defaultConfig(portOf(t, srv)))
	got := make(chan *Message, 8)
	client.OnMessage(func(m *Message) bool { return true }, func(c *Client, m *Message) {
		got <- m
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = client.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	srv.send(t, ":alice!a@host PRIVMSG #chan :hello world")
	srv.send(t, ":bob!b@host PRIVMSG ppwatch :help")

	m1 := <-got
	if m1.Nick != "alice" || !m1.ToChannel() || m1.Text != "hello world" {
		t.Errorf("channel msg wrong: %+v", m1)
	}
	m2 := <-got
	if m2.Nick != "bob" || m2.ToChannel() || m2.Text != "help" {
		t.Errorf("private msg wrong: %+v", m2)
	}
}

func TestSplitMessage(t *testing.T) {
	long := strings.Repeat("word ", 120) // 600 chars
	chunks := splitMessage(long, 400)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	joined := ""
	for i, c := range chunks {
		if len(c) > 400 {
			t.Errorf("chunk %d too long: %d", i, len(c))
		}
		if i > 0 {
			joined += " "
		}
		joined += c
	}
	if strings.Count(joined, "word") != 120 {
		t.Errorf("content lost in split: %d words", strings.Count(joined, "word"))
	}
}
