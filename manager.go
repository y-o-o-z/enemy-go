package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var _ = rand.IntN // keep import for PickRandom

// ManagerConfig holds runtime knobs the user controls via flags / commands.
type ManagerConfig struct {
	Pool        *LocalIPPool
	Servers     []IRCnetServer
	Mode        IPMode
	IRCPort     int
	JoinChans   []string
	Realnames   []string
	QuitReasons []string
	KickReasons []string
	Stagger     time.Duration
	Oident      *OidentManager

	Log func(string, ...any)
}

// PickKickReason returns a random configured kick reason, or "bye" as a
// safe fallback if the list is empty.
func (m *Manager) PickKickReason() string {
	m.mu.Lock()
	list := m.cfg.KickReasons
	m.mu.Unlock()
	if r := PickString(list); r != "" {
		return r
	}
	return "bye"
}

// KickReasons returns a copy of the configured kick reasons (read-only view).
func (m *Manager) KickReasons() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.cfg.KickReasons))
	copy(out, m.cfg.KickReasons)
	return out
}

// Manager owns the live clone set and offers higher-level operations.
type Manager struct {
	cfg ManagerConfig

	mu     sync.Mutex
	clones map[int]*Clone
	nextID atomic.Int64

	v4Idx  atomic.Uint64
	v6Idx  atomic.Uint64
	famIdx atomic.Uint64
}

func NewManager(cfg ManagerConfig) *Manager {
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	return &Manager{cfg: cfg, clones: map[int]*Clone{}}
}

// Spawn starts n new clones. Mode controls family balance; for ModeBoth we
// alternate 50/50 (or whatever's available).
func (m *Manager) Spawn(n int) ([]*Clone, error) {
	if n <= 0 {
		return nil, nil
	}
	if len(m.cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers loaded — fetch the server list first")
	}
	v4Servers, v6Servers := PartitionByFamily(m.cfg.Servers)
	switch m.cfg.Mode {
	case ModeV4:
		if len(v4Servers) == 0 {
			return nil, fmt.Errorf("mode=ipv4 but no servers resolved to A records")
		}
		if !m.cfg.Pool.HasV4() {
			return nil, fmt.Errorf("mode=ipv4 but no local IPv4 addresses in pool")
		}
	case ModeV6:
		if len(v6Servers) == 0 {
			return nil, fmt.Errorf("mode=ipv6 but no servers resolved to AAAA records")
		}
		if !m.cfg.Pool.HasV6() {
			return nil, fmt.Errorf("mode=ipv6 but no local IPv6 addresses in pool")
		}
	default:
		if (len(v4Servers) == 0 && len(v6Servers) == 0) ||
			(!m.cfg.Pool.HasV4() && !m.cfg.Pool.HasV6()) {
			return nil, fmt.Errorf("no usable family in pool/server list")
		}
	}

	var spawned []*Clone
	for i := 0; i < n; i++ {
		family := m.pickFamily(v4Servers, v6Servers)
		var srv IRCnetServer
		var bind string
		switch family {
		case "v4":
			srv = v4Servers[int(m.v4Idx.Add(1)-1)%len(v4Servers)]
			bind = m.cfg.Pool.PickV4()
		case "v6":
			srv = v6Servers[int(m.v6Idx.Add(1)-1)%len(v6Servers)]
			bind = m.cfg.Pool.PickV6()
		}

		port := m.cfg.IRCPort
		if port == 0 {
			port = 6667
		}
		serverAddr := net.JoinHostPort(srv.Name, strconv.Itoa(port))

		id := int(m.nextID.Add(1))
		cfg := CloneConfig{
			ID:         id,
			Server:     serverAddr,
			LocalIP:    bind,
			Family:     family,
			Nick:       RandomNick(6, 9),
			User:       RandomIdent(4, 8),
			RealName:   PickString(m.cfg.Realnames),
			Channels:   append([]string(nil), m.cfg.JoinChans...),
			QuitReason: PickString(m.cfg.QuitReasons),
		}
		if cfg.RealName == "" {
			cfg.RealName = cfg.Nick
		}
		clone := NewClone(cfg, m.cfg.Oident, m.cfg.Log)

		m.mu.Lock()
		m.clones[id] = clone
		m.mu.Unlock()

		go clone.Run()
		spawned = append(spawned, clone)

		if m.cfg.Stagger > 0 && i+1 < n {
			time.Sleep(m.cfg.Stagger)
		}
	}
	return spawned, nil
}

// pickFamily decides v4 vs v6 for the next clone given Mode and what's
// actually available.
func (m *Manager) pickFamily(v4Servers, v6Servers []IRCnetServer) string {
	switch m.cfg.Mode {
	case ModeV4:
		return "v4"
	case ModeV6:
		return "v6"
	default:
		v4ok := len(v4Servers) > 0 && m.cfg.Pool.HasV4()
		v6ok := len(v6Servers) > 0 && m.cfg.Pool.HasV6()
		switch {
		case v4ok && v6ok:
			// alternate strictly so clones split 50/50 over time
			if m.famIdx.Add(1)%2 == 0 {
				return "v4"
			}
			return "v6"
		case v4ok:
			return "v4"
		default:
			return "v6"
		}
	}
}

func (m *Manager) Snapshot() []*Clone {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		out = append(out, c)
	}
	return out
}

func (m *Manager) Get(id int) *Clone {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clones[id]
}

func (m *Manager) Count() (total, online int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clones {
		total++
		if c.State() == StateRegistered {
			online++
		}
	}
	return
}

// JoinAll instructs every clone (current + future via state) to join channel.
// Future-clones are handled by adding it to cfg.JoinChans before next Spawn.
func (m *Manager) JoinAll(ch string) {
	m.mu.Lock()
	if !containsFold(m.cfg.JoinChans, ch) {
		m.cfg.JoinChans = append(m.cfg.JoinChans, ch)
	}
	clones := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		clones = append(clones, c)
	}
	m.mu.Unlock()
	for _, c := range clones {
		c.Join(ch)
	}
}

func (m *Manager) PartAll(ch string) {
	m.mu.Lock()
	out := m.cfg.JoinChans[:0]
	for _, c := range m.cfg.JoinChans {
		if !strings.EqualFold(c, ch) {
			out = append(out, c)
		}
	}
	m.cfg.JoinChans = out
	clones := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		clones = append(clones, c)
	}
	m.mu.Unlock()
	for _, c := range clones {
		c.Part(ch)
	}
}

func (m *Manager) Broadcast(do func(*Clone)) {
	m.mu.Lock()
	clones := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		clones = append(clones, c)
	}
	m.mu.Unlock()
	for _, c := range clones {
		do(c)
	}
}

// PickRandom returns one online clone, or nil.
func (m *Manager) PickRandom() *Clone {
	m.mu.Lock()
	candidates := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		if c.State() == StateRegistered {
			candidates = append(candidates, c)
		}
	}
	m.mu.Unlock()
	if len(candidates) == 0 {
		return nil
	}
	return candidates[rand.IntN(len(candidates))]
}

func (m *Manager) Remove(id int) bool {
	m.mu.Lock()
	c, ok := m.clones[id]
	if ok {
		delete(m.clones, id)
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.QuitWithCtx(ctx)
	return true
}

// QuitAll asks every clone to disconnect and waits up to ctx for them.
func (m *Manager) QuitAll(ctx context.Context) {
	m.mu.Lock()
	clones := make([]*Clone, 0, len(m.clones))
	for _, c := range m.clones {
		clones = append(clones, c)
	}
	m.clones = map[int]*Clone{}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, c := range clones {
		wg.Add(1)
		go func(c *Clone) {
			defer wg.Done()
			c.QuitWithCtx(ctx)
		}(c)
	}
	doneCh := make(chan struct{})
	go func() { wg.Wait(); close(doneCh) }()
	select {
	case <-doneCh:
	case <-ctx.Done():
	}
}

// SwapMode reconfigures the family selector. Existing clones keep their
// bindings; only newly-spawned clones honour the new mode.
func (m *Manager) SwapMode(mode IPMode) {
	m.mu.Lock()
	m.cfg.Mode = mode
	m.mu.Unlock()
}

func (m *Manager) Mode() IPMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Mode
}

// Oident returns the active ident broker, or nil if oidentd integration
// is disabled. Used by the shell for status reporting.
func (m *Manager) Oident() *OidentManager {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Oident
}

// SwapServers updates the resolved server list (e.g. after a refresh).
func (m *Manager) SwapServers(servers []IRCnetServer) {
	m.mu.Lock()
	m.cfg.Servers = servers
	m.mu.Unlock()
}

func (m *Manager) Servers() []IRCnetServer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]IRCnetServer, len(m.cfg.Servers))
	copy(out, m.cfg.Servers)
	return out
}

// JoinedChans returns the channels every newly-spawned clone currently
// auto-joins. Existing clones may have additional state.
func (m *Manager) JoinedChans() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.cfg.JoinChans))
	copy(out, m.cfg.JoinChans)
	return out
}

func containsFold(xs []string, s string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
