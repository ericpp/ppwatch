// Package irc provides a minimal IRC bot client with the features ppwatch
// needs: TLS, registration with nick-collision fallback, PING keepalive,
// NickServ await, and message handler dispatch.
package irc

import (
	"crypto/tls"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Message is a parsed IRC message relevant to the bot.
type Message struct {
	Prefix  string   // raw prefix, e.g. "nick!user@host"
	Nick    string   // sender nick parsed from the prefix
	Command string   // IRC command or numeric
	Params  []string // message parameters
	Text    string   // trailing (last) parameter
}

// Recipient returns the first parameter (the message target).
func (m *Message) Recipient() string {
	if len(m.Params) > 0 {
		return m.Params[0]
	}
	return ""
}

// ToChannel reports whether the message was sent to a channel (as opposed
// to a private message to the bot itself).
func (m *Message) ToChannel() bool {
	r := m.Recipient()
	return strings.HasPrefix(r, "#") || strings.HasPrefix(r, "&")
}

// Matcher decides whether a handler should see a message.
type Matcher func(*Message) bool

// MessageHandler handles an incoming message.
type MessageHandler func(*Client, *Message)

// ConnectedHandler runs once per connection after registration completes.
type ConnectedHandler func(*Client) error

// Config configures a Client.
type Config struct {
	Host     string
	Port     int
	Secure   bool
	Nick     string
	User     string
	Realname string
	// ReadTimeout is the socket read deadline; a PING keepalive fires at
	// half this interval to keep the connection alive.
	ReadTimeout time.Duration
	// KeepaliveInterval overrides the default PING interval when non-zero.
	KeepaliveInterval time.Duration
}

type awaiter struct {
	sender string
	re     *regexp.Regexp
	ch     chan *Message
}

type handlerEntry struct {
	match Matcher
	fn    MessageHandler
}

// Client is a minimal IRC bot client.
type Client struct {
	cfg Config

	conn    net.Conn
	writeMu sync.Mutex

	mu         sync.Mutex
	actualNick string

	onConnected []ConnectedHandler
	handlers    []handlerEntry
	awaiters    []*awaiter

	done chan struct{}
}

// NewClient creates a client from the config.
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, done: make(chan struct{})}
}

// OnConnected registers a handler invoked after registration (376/422).
// Returning an error drops the connection and surfaces it from Run.
func (c *Client) OnConnected(fn ConnectedHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onConnected = append(c.onConnected, fn)
}

// OnMessage registers a message handler with a matcher.
func (c *Client) OnMessage(match Matcher, fn MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, handlerEntry{match: match, fn: fn})
}

// Nick returns the server-assigned nickname (which may differ from the
// requested one if a collision fallback applied).
func (c *Client) Nick() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.actualNick
}

// AwaitMessage arranges delivery of the next message from the given sender
// matching re. Call the returned cancel function to release it unused.
func (c *Client) AwaitMessage(sender string, re *regexp.Regexp, timeout time.Duration) (<-chan *Message, func()) {
	ch := make(chan *Message, 1)
	a := &awaiter{sender: sender, re: re, ch: ch}
	c.mu.Lock()
	c.awaiters = append(c.awaiters, a)
	c.mu.Unlock()

	timer := time.AfterFunc(timeout, func() { c.removeAwaiter(a) })
	cancel := func() {
		timer.Stop()
		c.removeAwaiter(a)
	}
	return ch, cancel
}

func (c *Client) removeAwaiter(a *awaiter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.awaiters {
		if x == a {
			c.awaiters = append(c.awaiters[:i], c.awaiters[i+1:]...)
			break
		}
	}
}

// Join sends a JOIN command.
func (c *Client) Join(channel string) error {
	return c.Send("JOIN " + channel)
}

// Message sends a PRIVMSG, splitting long texts at word boundaries so no
// line exceeds the protocol limit.
func (c *Client) Message(target, text string) error {
	for _, chunk := range splitMessage(text, 400) {
		if err := c.Send("PRIVMSG " + target + " :" + chunk); err != nil {
			return err
		}
	}
	return nil
}

// Send writes a raw line to the server.
func (c *Client) Send(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.conn == nil {
		return ErrNotConnected
	}
	if _, err := c.conn.Write([]byte(line + "\r\n")); err != nil {
		return err
	}
	return nil
}

// Close closes the connection.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// splitMessage splits text into chunks of at most limit runes, preferring
// spaces, and always making progress.
func splitMessage(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var chunks []string
	for len(text) > limit {
		cut := limit
		// try to break at the last space within the limit
		if idx := strings.LastIndex(text[:limit], " "); idx > 0 {
			cut = idx
		}
		chunks = append(chunks, strings.TrimRight(text[:cut], " "))
		text = strings.TrimLeft(text[cut:], " ")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}

// dial establishes the connection (TLS when configured).
func (c *Client) dial() (net.Conn, error) {
	addr := net.JoinHostPort(c.cfg.Host, itoa(c.cfg.Port))
	if c.cfg.Secure {
		return tls.Dial("tcp", addr, &tls.Config{ServerName: c.cfg.Host})
	}
	return net.Dial("tcp", addr)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
