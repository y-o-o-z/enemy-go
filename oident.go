package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// OidentManager manages a per-user ~/.oidentd.conf so each clone can
// present a distinct ident to the IRC server even though they all run as
// the same UNIX user.
//
// Key insight: IRCnet's per-(ident@host) limits mean that the practical
// connection cap can be raised by varying the ident across clones. With a
// running oidentd, the daemon answers ident lookups from the IRC server
// using the rules in this user's ~/.oidentd.conf. We write rules keyed on
// (local-bind-IP, remote-host) so each (IP, server) pair gets its own
// random ident. Within a managed block bracketed by sentinel comments —
// any user content outside the block is preserved.
//
// LIMITATION: go-ircevo does not let us pin a local source port, so we
// cannot key rules on lport. Rules therefore vary per (local IP, remote
// host) but NOT per individual TCP connection. With M servers and N local
// IPs you get up to N*M distinct idents concurrently; further clones that
// land on an already-bound (IP, server) tuple will share that tuple's
// ident. That's still the default behaviour minus this feature, so it's
// strictly an improvement.
type OidentManager struct {
	path string

	mu      sync.Mutex
	entries map[string]string // "from|to" -> ident
	closed  bool

	log func(string, ...any)
}

const (
	oidentBeginMarker = "# >>> enemy-go managed (do not edit between markers) >>>"
	oidentEndMarker   = "# <<< enemy-go managed <<<"
)

// NewOidentManager opens (or creates) the user's oidentd config file and
// initialises an empty managed block. The file outside the managed block
// is preserved verbatim. Returns an error if the file isn't writable.
func NewOidentManager(path string, log func(string, ...any)) (*OidentManager, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	expanded, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	if err := ensureWritable(expanded); err != nil {
		return nil, err
	}
	m := &OidentManager{
		path:    expanded,
		entries: map[string]string{},
		log:     log,
	}
	if err := m.flushLocked(); err != nil {
		return nil, fmt.Errorf("oidentd: write %s: %w", expanded, err)
	}
	return m, nil
}

// DetectOidentd probes for a running oidentd by attempting a TCP connect
// to 127.0.0.1:113. Used by the "auto" mode to decide whether to enable
// the integration.
func DetectOidentd() bool {
	for _, addr := range []string{"127.0.0.1:113", "[::1]:113"} {
		c, err := net.DialTimeout("tcp", addr, 800*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

// Reserve returns the ident assigned to the (localIP, remoteHost) pair,
// generating a new random one and persisting it on first use. Concurrent
// calls for the same key are safe and will all see the same ident.
func (m *OidentManager) Reserve(localIP, remoteHost string) (string, error) {
	if m == nil {
		return "", nil
	}
	key := keyFor(localIP, remoteHost)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", fmt.Errorf("oidentd manager closed")
	}
	if id, ok := m.entries[key]; ok {
		return id, nil
	}
	id := RandomIdent(4, 8)
	m.entries[key] = id
	if err := m.flushLocked(); err != nil {
		delete(m.entries, key)
		return "", err
	}
	m.log("[oident] reserve %-39s → %-39s ident=%s", localIP, remoteHost, id)
	return id, nil
}

// Close removes every entry written by this manager, leaving the user's
// oidentd.conf clean. Safe to call multiple times.
func (m *OidentManager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.entries = map[string]string{}
	m.closed = true
	return m.flushLocked()
}

// Path is exposed so the operator can inspect what we managed.
func (m *OidentManager) Path() string {
	if m == nil {
		return ""
	}
	return m.path
}

// Count returns how many ident reservations are live.
func (m *OidentManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

func keyFor(localIP, remoteHost string) string {
	return strings.ToLower(localIP) + "|" + strings.ToLower(remoteHost)
}

// flushLocked rewrites the config file, replacing the managed block
// while preserving any other lines. Caller must hold m.mu.
func (m *OidentManager) flushLocked() error {
	pre, post, err := readPreservedSections(m.path)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".oidentd.conf.tmp.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	w := bufio.NewWriter(tmp)
	if pre != "" {
		if _, err := w.WriteString(pre); err != nil {
			return err
		}
		if !strings.HasSuffix(pre, "\n") {
			_, _ = w.WriteString("\n")
		}
	}
	if _, err := fmt.Fprintln(w, oidentBeginMarker); err != nil {
		return err
	}
	keys := make([]string, 0, len(m.entries))
	for k := range m.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			continue
		}
		fromIP, toHost := parts[0], parts[1]
		ident := m.entries[k]
		fmt.Fprintf(w, "to \"%s\" from \"%s\" { reply \"%s\" force }\n", escapeQuotes(toHost), escapeQuotes(fromIP), escapeQuotes(ident))
	}
	if _, err := fmt.Fprintln(w, oidentEndMarker); err != nil {
		return err
	}
	if post != "" {
		if !strings.HasPrefix(post, "\n") {
			_, _ = w.WriteString("\n")
		}
		if _, err := w.WriteString(post); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), m.path)
}

// readPreservedSections reads the existing config and returns the parts
// before and after our managed block. If no managed block exists, the
// entire file goes into "pre".
func readPreservedSections(path string) (pre, post string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	text := string(data)
	beginIdx := strings.Index(text, oidentBeginMarker)
	if beginIdx < 0 {
		return text, "", nil
	}
	pre = text[:beginIdx]
	rest := text[beginIdx:]
	endIdx := strings.Index(rest, oidentEndMarker)
	if endIdx < 0 {
		// stale file with no end marker — treat the whole tail as ours.
		return pre, "", nil
	}
	tail := rest[endIdx+len(oidentEndMarker):]
	tail = strings.TrimLeft(tail, "\n")
	return pre, tail, nil
}

func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, ``)
}

func expandHome(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty oidentd config path")
	}
	if path[0] != '~' {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func ensureWritable(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}
