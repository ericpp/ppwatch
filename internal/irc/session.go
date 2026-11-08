package irc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ErrNotConnected is returned by Send before a connection exists.
var ErrNotConnected = errors.New("not connected")

// Run dials the server, completes registration and processes messages until
// the connection is lost, the context is cancelled, or a connected handler
// returns an error. It returns the error that ended the session.
func (c *Client) Run(ctx context.Context) error {
	conn, err := c.dial()
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	c.conn = conn

	readTimeout := c.cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 300 * time.Second
	}
	_ = conn.SetDeadline(time.Now().Add(readTimeout))

	defer c.Close()

	// Register.
	nick := c.cfg.Nick
	if nick == "" {
		nick = "ppwatch"
	}
	user := c.cfg.User
	if user == "" {
		user = nick
	}
	realname := c.cfg.Realname
	if realname == "" {
		realname = nick
	}
	if err := c.Send("NICK " + nick); err != nil {
		return err
	}
	if err := c.Send(fmt.Sprintf("USER %s 0 * :%s", user, realname)); err != nil {
		return err
	}

	lines := make(chan string, 64)
	readErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 64*1024), 64*1024)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			default:
			}
			lines <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			readErr <- err
		} else {
			readErr <- errors.New("connection closed by server")
		}
	}()
	// Wait for the reader BEFORE the connection close deferred below so the
	// close unblocks the blocked scanner read.
	defer wg.Wait()
	defer c.Close()

	// Messages are dispatched off the read loop so handlers may block -
	// e.g. awaiting a NickServ reply - without stalling protocol traffic.
	msgChan := make(chan *Message, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case m := <-msgChan:
				c.dispatch(m)
			}
		}
	}()

	// onConnected handlers run in their own goroutine; an error is
	// delivered to fail and ends the session (triggering a reconnect).
	fail := make(chan error, 1)
	startConnected := func() {
		c.mu.Lock()
		connected := append([]ConnectedHandler(nil), c.onConnected...)
		c.mu.Unlock()
		go func() {
			for _, fn := range connected {
				if err := fn(c); err != nil {
					select {
					case fail <- err:
					default:
					}
					return
				}
			}
		}()
	}

	keepalive := c.cfg.KeepaliveInterval
	if keepalive <= 0 {
		keepalive = readTimeout / 2
		if keepalive > time.Minute {
			keepalive = time.Minute
		}
	}
	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()

	registered := false
	triedFallbackNick := false

	for {
		select {
		case <-ctx.Done():
			c.Send("QUIT :shutting down")
			return ctx.Err()

		case err := <-readErr:
			return err

		case err := <-fail:
			return err

		case <-ticker.C:
			if err := c.Send("PING :ppwatch-keepalive"); err != nil {
				return err
			}

		case line := <-lines:
			_ = conn.SetDeadline(time.Now().Add(readTimeout))

			msg := parseLine(line)
			if msg == nil {
				continue
			}

			switch msg.Command {
			case "PING":
				token := msg.Text
				if token == "" && len(msg.Params) > 0 {
					token = msg.Params[0]
				}
				if err := c.Send("PONG :" + token); err != nil {
					return err
				}
				continue

			case "433": // nick in use (during registration)
				if !registered && !triedFallbackNick {
					triedFallbackNick = true
					log.Printf("Nickname %s is taken, retrying as %s_", nick, nick)
					if err := c.Send("NICK " + nick + "_"); err != nil {
						return err
					}
					continue
				}

			case "001":
				if len(msg.Params) > 0 {
					c.mu.Lock()
					c.actualNick = msg.Params[0]
					c.mu.Unlock()
				}

			case "376", "422": // end of MOTD or no MOTD
				if !registered {
					registered = true
					log.Printf("Connected to %s", c.cfg.Host)
					startConnected()
				}
			}

			if msg.Command == "PRIVMSG" {
				msgChan <- msg
			} else if msg.Command == "NOTICE" {
				// Notices (e.g. NickServ replies) are only relevant to
				// awaiters; they do not run message handlers.
				c.matchAwaiters(msg)
			}
		}
	}
}

// dispatch routes a PRIVMSG to matching awaiters and handlers.
func (c *Client) dispatch(m *Message) {
	c.matchAwaiters(m)

	c.mu.Lock()
	entries := append([]handlerEntry(nil), c.handlers...)
	c.mu.Unlock()

	for _, h := range entries {
		if h.match(m) {
			h.fn(c, m)
		}
	}
}

// matchAwaiters delivers the message to any matching registered awaiter.
func (c *Client) matchAwaiters(m *Message) {
	c.mu.Lock()
	awaiters := append([]*awaiter(nil), c.awaiters...)
	c.mu.Unlock()

	for _, a := range awaiters {
		if a.sender != "" && !strings.EqualFold(m.Nick, a.sender) {
			continue
		}
		if a.re != nil && !a.re.MatchString(m.Text) {
			continue
		}
		select {
		case a.ch <- m:
		default:
		}
	}
}

// parseLine parses one IRC protocol line into a Message; returns nil for
// empty lines.
func parseLine(line string) *Message {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil
	}

	m := &Message{}
	rest := line
	if strings.HasPrefix(rest, ":") {
		space := strings.Index(rest, " ")
		if space < 0 {
			m.Prefix = rest[1:]
			return m
		}
		m.Prefix = rest[1:space]
		rest = rest[space+1:]
		if bang := strings.Index(m.Prefix, "!"); bang >= 0 {
			m.Nick = m.Prefix[:bang]
		} else {
			m.Nick = m.Prefix
		}
	}

	var trailing string
	hasTrailing := false
	if idx := strings.Index(rest, " :"); idx >= 0 {
		trailing = rest[idx+2:]
		hasTrailing = true
		rest = rest[:idx]
	}

	params := strings.Fields(rest)
	m.Command = strings.ToUpper(params[0])
	m.Params = params[1:]
	if hasTrailing {
		m.Params = append(m.Params, trailing)
		m.Text = trailing
	}
	return m
}
