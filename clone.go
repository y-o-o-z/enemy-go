package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	irc "github.com/kofany/go-ircevo"
)

// CloneState describes a clone's lifecycle phase.
type CloneState int32

const (
	StateInit CloneState = iota
	StateConnecting
	StateRegistered
	StateDisconnected
	StateQuit
)

func (s CloneState) String() string {
	switch s {
	case StateInit:
		return "init"
	case StateConnecting:
		return "connecting"
	case StateRegistered:
		return "online"
	case StateDisconnected:
		return "off"
	case StateQuit:
		return "quit"
	}
	return "?"
}

// CloneConfig is the immutable subset of settings each clone uses to come
// up. The shared `*Manager` adds nick generation and channel state.
type CloneConfig struct {
	ID         int
	Server     string // host:port
	LocalIP    string // bind source (IPv4 or IPv6)
	Family     string // "v4" or "v6"
	Nick       string
	User       string
	RealName   string
	Channels   []string
	QuitReason string
}

// Clone wraps a single go-ircevo Connection plus our reconnect/rejoin loop.
type Clone struct {
	cfg  CloneConfig
	conn *irc.Connection

	state atomic.Int32
	mu    sync.Mutex

	// channels we *want* to be in. Driven by the manager via Join/Part.
	channels map[string]bool

	stop chan struct{}
	done chan struct{}

	log func(format string, args ...any)
}

func NewClone(cfg CloneConfig, log func(string, ...any)) *Clone {
	c := &Clone{
		cfg:      cfg,
		channels: make(map[string]bool),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		log:      log,
	}
	for _, ch := range cfg.Channels {
		c.channels[strings.ToLower(ch)] = true
	}
	c.state.Store(int32(StateInit))
	return c
}

func (c *Clone) State() CloneState {
	return CloneState(c.state.Load())
}

func (c *Clone) setState(s CloneState) {
	c.state.Store(int32(s))
}

// Run drives the connect → loop → reconnect lifecycle until Stop() is called.
// Each pass creates a fresh *irc.Connection because go-ircevo's internal
// state isn't reusable after Disconnect.
func (c *Clone) Run() {
	defer close(c.done)
	backoff := 5 * time.Second
	const maxBackoff = 90 * time.Second

	for {
		select {
		case <-c.stop:
			return
		default:
		}

		err := c.connectOnce()
		if err == nil {
			// connectOnce only returns when Loop() exited (disconnect or stop).
			if c.State() == StateQuit {
				return
			}
			backoff = 5 * time.Second
		} else {
			c.log("[clone %d] connect error: %v", c.cfg.ID, err)
		}

		c.setState(StateDisconnected)

		select {
		case <-c.stop:
			return
		case <-time.After(backoff + time.Duration(c.cfg.ID%5)*time.Second):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Clone) connectOnce() error {
	conn := irc.IRC(c.cfg.Nick, c.cfg.User)
	conn.RealName = c.cfg.RealName
	conn.QuitMessage = c.cfg.QuitReason
	conn.Timeout = 20 * time.Second
	conn.PingFreq = 4 * time.Minute
	conn.KeepAlive = 4 * time.Minute
	conn.Version = "enemy-go"
	if c.cfg.LocalIP != "" {
		conn.SetLocalIP(c.cfg.LocalIP)
	}

	conn.AddCallback("001", func(e *irc.Event) {
		c.setState(StateRegistered)
		c.mu.Lock()
		chs := make([]string, 0, len(c.channels))
		for ch := range c.channels {
			chs = append(chs, ch)
		}
		c.mu.Unlock()
		for _, ch := range chs {
			conn.Join(ch)
		}
	})

	// auto-rejoin on KICK if we're the kicked party
	conn.AddCallback("KICK", func(e *irc.Event) {
		if len(e.Arguments) < 2 {
			return
		}
		ch := e.Arguments[0]
		who := e.Arguments[1]
		if !strings.EqualFold(who, conn.GetNick()) {
			return
		}
		c.mu.Lock()
		want := c.channels[strings.ToLower(ch)]
		c.mu.Unlock()
		if want {
			time.AfterFunc(3*time.Second, func() {
				if c.State() == StateRegistered {
					conn.Join(ch)
				}
			})
		}
	})

	conn.AddCallback("ERROR", func(e *irc.Event) {
		c.log("[clone %d] server ERROR: %s", c.cfg.ID, e.Message())
	})

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.setState(StateConnecting)
	if err := conn.Connect(c.cfg.Server); err != nil {
		return err
	}

	// We deliberately do NOT call conn.Loop(). go-ircevo's Loop runs its own
	// auto-reconnect, which collides with our outer backoff in Run() and
	// also panics on double-close of the internal channels. Instead we read
	// the error channel ourselves and let Run() handle the backoff.
	errCh := conn.ErrorChan()
	select {
	case err := <-errCh:
		c.log("[clone %d] disconnected: %v", c.cfg.ID, err)
		safeDisconnect(conn)
		return nil
	case <-c.stop:
		conn.QuitMessage = c.cfg.QuitReason
		conn.Quit() // sends QUIT and sets quit=true
		// drain (or time-out waiting for) the resulting error event
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
		safeDisconnect(conn)
		return nil
	}
}

// safeDisconnect calls Disconnect at most once, swallowing the panic that
// would result if go-ircevo's internals already closed the same channels.
func safeDisconnect(conn *irc.Connection) {
	defer func() { _ = recover() }()
	conn.Disconnect()
}

func (c *Clone) Stop() {
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
	c.setState(StateQuit)
}

func (c *Clone) Wait() { <-c.done }

// withConn runs fn against the live connection if we have one.
func (c *Clone) withConn(fn func(conn *irc.Connection)) bool {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil || c.State() != StateRegistered {
		return false
	}
	fn(conn)
	return true
}

func (c *Clone) Join(ch string) {
	c.mu.Lock()
	c.channels[strings.ToLower(ch)] = true
	c.mu.Unlock()
	c.withConn(func(conn *irc.Connection) { conn.Join(ch) })
}

func (c *Clone) Part(ch string) {
	c.mu.Lock()
	delete(c.channels, strings.ToLower(ch))
	c.mu.Unlock()
	c.withConn(func(conn *irc.Connection) { conn.Part(ch) })
}

func (c *Clone) Privmsg(target, msg string) bool {
	return c.withConn(func(conn *irc.Connection) { conn.Privmsg(target, msg) })
}

func (c *Clone) Notice(target, msg string) bool {
	return c.withConn(func(conn *irc.Connection) { conn.Notice(target, msg) })
}

func (c *Clone) SendRaw(line string) bool {
	return c.withConn(func(conn *irc.Connection) { conn.SendRaw(line) })
}

func (c *Clone) Mode(target string, modes ...string) bool {
	return c.withConn(func(conn *irc.Connection) { conn.Mode(target, modes...) })
}

func (c *Clone) Nick() string {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return c.cfg.Nick
	}
	if n := conn.GetNick(); n != "" {
		return n
	}
	return c.cfg.Nick
}

// SafeServerName returns "host:port" for status output. We strip an IPv6
// bracketed form back to the original hostname when possible.
func (c *Clone) Server() string {
	host, port, err := net.SplitHostPort(c.cfg.Server)
	if err != nil {
		return c.cfg.Server
	}
	return fmt.Sprintf("%s:%s", host, port)
}

func (c *Clone) Info() string {
	return fmt.Sprintf("#%-3d %-9s %-4s bind=%-39s nick=%-15s server=%s",
		c.cfg.ID, c.State(), c.cfg.Family, c.cfg.LocalIP, c.Nick(), c.Server())
}

// Used by manager to time staggered connects.
func WaitJitter(base, jitter time.Duration) {
	if jitter <= 0 {
		time.Sleep(base)
		return
	}
	time.Sleep(base + time.Duration(int64(jitter)*int64(time.Now().UnixNano()%1000)/1000))
}

// QuitWithCtx asks the clone to disconnect cleanly within a deadline.
func (c *Clone) QuitWithCtx(ctx context.Context) {
	c.Stop()
	done := make(chan struct{})
	go func() { c.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
