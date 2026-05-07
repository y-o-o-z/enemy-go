package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOidentManagerPreservesUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oidentd.conf")
	if err := os.WriteFile(path, []byte("# user rule, keep me\nfrom \"1.2.3.4\" { reply \"keepme\" force }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := NewOidentManager(path, func(string, ...any) {})
	if err != nil {
		t.Fatalf("NewOidentManager: %v", err)
	}
	id1, err := m.Reserve("203.0.113.5", "irc.example.org")
	if err != nil || id1 == "" {
		t.Fatalf("Reserve: id=%q err=%v", id1, err)
	}
	id2, err := m.Reserve("203.0.113.5", "irc.example.org")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("same key returned different idents: %s vs %s", id1, id2)
	}
	id3, err := m.Reserve("203.0.113.5", "other.example.org")
	if err != nil || id3 == "" || id3 == id1 && false {
		// id3 may or may not equal id1 (random collisions tolerated); just check non-empty
		t.Fatalf("Reserve other: id=%q err=%v", id3, err)
	}

	data, _ := os.ReadFile(path)
	body := string(data)
	if !strings.Contains(body, "keep me") {
		t.Fatalf("user content was clobbered:\n%s", body)
	}
	if !strings.Contains(body, oidentBeginMarker) || !strings.Contains(body, oidentEndMarker) {
		t.Fatalf("managed block markers missing:\n%s", body)
	}
	if !strings.Contains(body, "irc.example.org") {
		t.Fatalf("rule for irc.example.org missing:\n%s", body)
	}

	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	body = string(data)
	if strings.Contains(body, "irc.example.org") {
		t.Fatalf("Close did not remove our rules:\n%s", body)
	}
	if !strings.Contains(body, "keep me") {
		t.Fatalf("Close clobbered user content:\n%s", body)
	}
}

func TestOidentManagerCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oidentd.conf")
	m, err := NewOidentManager(path, func(string, ...any) {})
	if err != nil {
		t.Fatalf("NewOidentManager: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
	_ = m.Close()
}

func TestOidentDetectorDoesNotPanic(t *testing.T) {
	// Just exercise the probe — no assertion on result, since it depends
	// on whether the test host is running oidentd.
	_ = DetectOidentd()
}
