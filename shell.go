package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Shell drives the interactive operator interface.
type Shell struct {
	mgr  *Manager
	out  io.Writer
	in   *bufio.Reader
	url  string
	stop chan struct{}
}

func NewShell(mgr *Manager, serverListURL string) *Shell {
	return &Shell{
		mgr:  mgr,
		out:  os.Stdout,
		in:   bufio.NewReader(os.Stdin),
		url:  serverListURL,
		stop: make(chan struct{}),
	}
}

func (s *Shell) printf(format string, args ...any) {
	fmt.Fprintf(s.out, format, args...)
}

// Run is blocking. When the user issues "exit"/"quit", Run returns.
func (s *Shell) Run() {
	s.printf("Type 'help' for available commands.\n")
	for {
		fmt.Fprint(s.out, "enemy> ")
		line, err := s.in.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				s.printf("\n")
				return
			}
			s.printf("[!] stdin: %v\n", err)
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "/") || strings.HasPrefix(line, ".") {
			line = line[1:]
		}
		if !s.dispatch(line) {
			return
		}
	}
}

func (s *Shell) dispatch(line string) bool {
	args := splitArgs(line)
	if len(args) == 0 {
		return true
	}
	cmd := strings.ToLower(args[0])
	rest := args[1:]
	switch cmd {
	case "help", "h", "?":
		s.cmdHelp()
	case "exit", "quit", "q", "bye":
		s.cmdQuit()
		return false
	case "load", "spawn", "clones":
		s.cmdLoad(rest)
	case "stat", "status", "s", "list", "ls":
		s.cmdStat(rest)
	case "join", "j":
		s.cmdJoin(rest)
	case "part", "p", "leave":
		s.cmdPart(rest)
	case "msg", "amsg":
		s.cmdMsg(rest, false)
	case "notice":
		s.cmdNotice(rest)
	case "say":
		s.cmdMsg(rest, true)
	case "raw", "quote":
		s.cmdRaw(rest)
	case "mode":
		s.cmdMode(rest)
	case "kick":
		s.cmdKick(rest)
	case "reasons":
		s.cmdReasons()
	case "op":
		s.cmdOpDeop(rest, true)
	case "deop":
		s.cmdOpDeop(rest, false)
	case "del", "kill", "remove":
		s.cmdDel(rest)
	case "ipmode":
		s.cmdIPMode(rest)
	case "servers", "srv":
		s.cmdServers(rest)
	case "refresh":
		s.cmdRefresh()
	case "pool", "ips":
		s.cmdPool()
	case "disco", "disconnect":
		s.cmdDisconnect()
	default:
		s.printf("[!] unknown command: %s (try 'help')\n", cmd)
	}
	return true
}

func (s *Shell) cmdHelp() {
	s.printf(`Available commands:
  load <N>                 spawn N more clones
  stat                     list all clones with state
  ipmode <ipv4|ipv6|both>  switch family for *future* clones
  servers [N]              show first N servers from cached list (default 30)
  refresh                  refetch server list from ircnet.info
  pool                     show local IPs available for binding
  join <#chan>             every clone joins
  part <#chan>             every clone parts
  msg <target> <text>      every clone PRIVMSG target
  notice <target> <text>   every clone NOTICE target
  say  <#chan> <text>      one random clone PRIVMSG
  raw  <line>              one random clone sends raw IRC line
  mode <target> <flags>    one random clone sets mode
  kick <#chan> <nick> [r]  one random clone tries KICK (random reason if [r] omitted)
  reasons                  list configured kick reasons
  op   [#chan] <nick...>   every online clone sends MODE +o (post-takeover op grant)
  deop [#chan] <nick...>   every online clone sends MODE -o
  del  <id>                disconnect & forget clone #id
  disco                    quit all clones (keeps shell)
  exit / quit              quit all clones and leave
`)
}

func (s *Shell) cmdQuit() {
	s.printf("[*] disconnecting all clones...\n")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s.mgr.QuitAll(ctx)
	s.printf("[*] bye.\n")
}

func (s *Shell) cmdLoad(args []string) {
	if len(args) < 1 {
		s.printf("usage: load <N>\n")
		return
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		s.printf("[!] N must be a positive integer\n")
		return
	}
	clones, err := s.mgr.Spawn(n)
	if err != nil {
		s.printf("[!] spawn: %v\n", err)
		return
	}
	s.printf("[+] spawned %d clones (mode=%s)\n", len(clones), s.mgr.Mode())
}

func (s *Shell) cmdStat(args []string) {
	clones := s.mgr.Snapshot()
	total, online := s.mgr.Count()
	s.printf("[*] %d clones (%d online), mode=%s\n", total, online, s.mgr.Mode())
	limit := len(clones)
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 && n < limit {
			limit = n
		}
	}
	for i, c := range clones {
		if i >= limit {
			s.printf("    ... %d more\n", len(clones)-limit)
			break
		}
		s.printf("    %s\n", c.Info())
	}
}

func (s *Shell) cmdJoin(args []string) {
	if len(args) < 1 {
		s.printf("usage: join <#channel>\n")
		return
	}
	s.mgr.JoinAll(args[0])
	s.printf("[+] all clones joining %s\n", args[0])
}

func (s *Shell) cmdPart(args []string) {
	if len(args) < 1 {
		s.printf("usage: part <#channel>\n")
		return
	}
	s.mgr.PartAll(args[0])
	s.printf("[-] all clones parting %s\n", args[0])
}

func (s *Shell) cmdMsg(args []string, randomOne bool) {
	if len(args) < 2 {
		s.printf("usage: msg <target> <text>\n")
		return
	}
	target := args[0]
	text := strings.Join(args[1:], " ")
	if randomOne {
		c := s.mgr.PickRandom()
		if c == nil {
			s.printf("[!] no online clones\n")
			return
		}
		c.Privmsg(target, text)
		return
	}
	sent := 0
	s.mgr.Broadcast(func(c *Clone) {
		if c.Privmsg(target, text) {
			sent++
		}
	})
	s.printf("[*] msg sent by %d clones\n", sent)
}

func (s *Shell) cmdNotice(args []string) {
	if len(args) < 2 {
		s.printf("usage: notice <target> <text>\n")
		return
	}
	target := args[0]
	text := strings.Join(args[1:], " ")
	sent := 0
	s.mgr.Broadcast(func(c *Clone) {
		if c.Notice(target, text) {
			sent++
		}
	})
	s.printf("[*] notice sent by %d clones\n", sent)
}

func (s *Shell) cmdRaw(args []string) {
	if len(args) < 1 {
		s.printf("usage: raw <IRC line>\n")
		return
	}
	c := s.mgr.PickRandom()
	if c == nil {
		s.printf("[!] no online clones\n")
		return
	}
	c.SendRaw(strings.Join(args, " "))
}

func (s *Shell) cmdMode(args []string) {
	if len(args) < 2 {
		s.printf("usage: mode <target> <flags...>\n")
		return
	}
	c := s.mgr.PickRandom()
	if c == nil {
		s.printf("[!] no online clones\n")
		return
	}
	c.Mode(args[0], args[1:]...)
}

func (s *Shell) cmdKick(args []string) {
	if len(args) < 2 {
		s.printf("usage: kick <#chan> <nick> [reason]\n")
		return
	}
	reason := s.mgr.PickKickReason()
	if len(args) >= 3 {
		reason = strings.Join(args[2:], " ")
	}
	c := s.mgr.PickRandom()
	if c == nil {
		s.printf("[!] no online clones\n")
		return
	}
	c.SendRaw(fmt.Sprintf("KICK %s %s :%s", args[0], args[1], reason))
}

func (s *Shell) cmdReasons() {
	rs := s.mgr.KickReasons()
	s.printf("[*] %d kick reason(s) loaded:\n", len(rs))
	for i, r := range rs {
		s.printf("    %d. %s\n", i+1, r)
	}
}

// cmdOpDeop implements the .op / .deop commands. Every online clone is
// asked to send MODE ±o for the given nicks. Clones that aren't opped will
// get 482 (ChanOpPrivsNeeded) from the server and effectively no-op; the
// opped clone(s) — typically the one(s) that won the takeover — succeed.
//
// Channel argument is optional when exactly one channel is auto-joined
// network-wide; otherwise it must be explicit.
//
// IRCnet (2.11) allows up to 3 mode params per MODE command, so we batch
// in groups of 3.
func (s *Shell) cmdOpDeop(args []string, give bool) {
	verb := "op"
	sign := "+"
	if !give {
		verb = "deop"
		sign = "-"
	}
	if len(args) < 1 {
		s.printf("usage: %s [#chan] <nick> [nick2 ...]\n", verb)
		return
	}
	var chn string
	var nicks []string
	if strings.HasPrefix(args[0], "#") || strings.HasPrefix(args[0], "&") {
		chn = args[0]
		nicks = args[1:]
	} else {
		chs := s.mgr.JoinedChans()
		if len(chs) != 1 {
			s.printf("[!] %d auto-joined channels — pass #chan explicitly\n", len(chs))
			return
		}
		chn = chs[0]
		nicks = args
	}
	if len(nicks) == 0 {
		s.printf("usage: %s [#chan] <nick> [nick2 ...]\n", verb)
		return
	}

	const maxPerMode = 3 // IRCnet 2.11 caps modes-per-command at 3
	sent := 0
	s.mgr.Broadcast(func(c *Clone) {
		if c.State() != StateRegistered {
			return
		}
		for i := 0; i < len(nicks); i += maxPerMode {
			end := i + maxPerMode
			if end > len(nicks) {
				end = len(nicks)
			}
			batch := nicks[i:end]
			flags := strings.Repeat("o", len(batch))
			c.SendRaw(fmt.Sprintf("MODE %s %s%s %s", chn, sign, flags, strings.Join(batch, " ")))
		}
		sent++
	})
	s.printf("[*] %s %s for %v issued by %d clones\n", verb, chn, nicks, sent)
}

func (s *Shell) cmdDel(args []string) {
	if len(args) < 1 {
		s.printf("usage: del <id>\n")
		return
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		s.printf("[!] invalid id\n")
		return
	}
	if s.mgr.Remove(id) {
		s.printf("[-] removed clone #%d\n", id)
	} else {
		s.printf("[!] no clone with id %d\n", id)
	}
}

func (s *Shell) cmdIPMode(args []string) {
	if len(args) < 1 {
		s.printf("current mode: %s\n", s.mgr.Mode())
		return
	}
	m, err := ParseIPMode(args[0])
	if err != nil {
		s.printf("[!] %v\n", err)
		return
	}
	s.mgr.SwapMode(m)
	s.printf("[*] mode → %s (existing clones unchanged)\n", m)
}

func (s *Shell) cmdServers(args []string) {
	servers := s.mgr.Servers()
	limit := 30
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			limit = n
		}
	}
	s.printf("[*] %d servers cached\n", len(servers))
	for i, sr := range servers {
		if i >= limit {
			s.printf("    ... %d more\n", len(servers)-limit)
			break
		}
		s.printf("    %s\n", FormatServerLine(sr))
	}
}

func (s *Shell) cmdRefresh() {
	s.printf("[*] fetching %s...\n", s.url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv, err := FetchIRCnetServers(ctx, s.url)
	if err != nil {
		s.printf("[!] fetch: %v\n", err)
		return
	}
	srv = ResolveServers(ctx, srv, s.mgr.Mode())
	s.mgr.SwapServers(srv)
	v4, v6 := PartitionByFamily(srv)
	s.printf("[+] %d servers (v4=%d, v6=%d)\n", len(srv), len(v4), len(v6))
}

func (s *Shell) cmdPool() {
	v4, v6 := s.mgr.cfg.Pool.Snapshot()
	s.printf("[*] local IPv4 (%d):\n", len(v4))
	for _, ip := range v4 {
		s.printf("    %s\n", ip)
	}
	s.printf("[*] local IPv6 (%d):\n", len(v6))
	for _, ip := range v6 {
		s.printf("    %s\n", ip)
	}
}

func (s *Shell) cmdDisconnect() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s.mgr.QuitAll(ctx)
	s.printf("[*] all clones disconnected\n")
}

// splitArgs is a tiny tokenizer that supports double-quoted strings so
// users can pass messages with spaces.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inq := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inq = !inq
		case (c == ' ' || c == '\t') && !inq:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
