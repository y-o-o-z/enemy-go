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
	s.printStartupMenu()
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

// printStartupMenu renders the post-launch operator console banner with a
// quick-reference of the most-used commands. The full reference lives in
// `help` (cmdHelp).
func (s *Shell) printStartupMenu() {
	total, online := s.mgr.Count()
	mode := s.mgr.Mode()
	v4, v6 := s.mgr.cfg.Pool.Counts()
	srvCount := len(s.mgr.Servers())

	const (
		reset  = "\x1b[0m"
		bold   = "\x1b[1m"
		dim    = "\x1b[2m"
		cyan   = "\x1b[36m"
		green  = "\x1b[32m"
		yellow = "\x1b[33m"
		bar    = "──────────────────────────────────────────────────────────────────────"
	)

	s.printf("\n%s%s%s\n", cyan, bar, reset)
	s.printf("  %senemy-go%s %s— IRCnet test-clones console%s\n", bold, reset, dim, reset)
	s.printf("%s%s%s\n", cyan, bar, reset)
	s.printf("  %sstatus%s   %s%d clones%s (%d online)   mode=%s%s%s   servers=%d   bind v4=%d v6=%d\n",
		bold, reset, green, total, reset, online, yellow, mode, reset, srvCount, v4, v6)
	s.printf("%s%s%s\n", cyan, bar, reset)
	s.printf("  %sQUICK COMMANDS%s\n", bold, reset)
	rows := [][2]string{
		{"load <N>", "spawn N additional clones"},
		{"stat", "list every clone (id, state, bind, nick, server)"},
		{"join / part #chan", "have all clones join or leave a channel"},
		{"msg / notice / say", "PRIVMSG / NOTICE from all clones (say = one random)"},
		{"raw <line>", "one random clone sends a raw IRC line"},
		{"mode <tgt> <flags>", "one random clone issues a MODE command"},
		{"kick <#chan> <nick>", "one random clone tries KICK (random reason)"},
		{"op / deop [#chan] <n>", "every online clone sends MODE +o / -o"},
		{"servers / refresh", "show or refetch the IRCnet server list"},
		{"pool / ipmode", "show local IPs / switch family for future spawns"},
		{"reasons", "list configured kick reasons"},
		{"del <id>", "disconnect and forget a single clone"},
		{"disco", "QUIT all clones, keep the shell"},
		{"exit / quit", "QUIT all clones and leave"},
	}
	for _, r := range rows {
		s.printf("    %s%-22s%s  %s\n", green, r[0], reset, r[1])
	}
	s.printf("%s%s%s\n", cyan, bar, reset)
	s.printf("  %stip:%s commands accept a leading '/' or '.' too — e.g. /load 3\n", dim, reset)
	s.printf("  %stip:%s `help` shows the full reference; Ctrl-C does a clean QUIT round-trip\n", dim, reset)
	s.printf("%s%s%s\n\n", cyan, bar, reset)
}

func (s *Shell) cmdHelp() {
	const bold = "\x1b[1m"
	const reset = "\x1b[0m"
	const cyan = "\x1b[36m"
	s.printf(`
%senemy-go — full command reference%s

%sCLONE LIFECYCLE%s
  load <N>                 spawn N more clones using the current mode/pool
  del <id>                 disconnect and forget clone #id (see 'stat' for ids)
  disco                    QUIT every clone but keep the shell alive
  exit | quit              QUIT every clone and leave

%sINSPECTION%s
  stat [N]                 list clones (state, family, bind IP, nick, server)
  pool                     show local IPv4/IPv6 addresses available for binding
  servers [N]              show first N cached IRCnet servers (default 30)
  refresh                  refetch the IRCnet server registry
  reasons                  list configured kick reasons
  ipmode [ipv4|ipv6|both]  show or change family policy for *future* spawns

%sIRC ACTIONS%s
  join <#chan>             every clone joins #chan (auto-rejoin on KICK)
  part <#chan>             every clone parts #chan
  msg <target> <text>      every clone sends PRIVMSG target :text
  notice <target> <text>   every clone sends NOTICE target :text
  say <target> <text>      one random clone sends PRIVMSG target :text
  raw <line>               one random clone sends a raw IRC line
  mode <target> <flags>    one random clone sets MODE flags on target
  kick <#chan> <nick> [r]  one random clone tries KICK; random reason if omitted
  op   [#chan] <nick...>   every online clone sends MODE +o (post-takeover)
  deop [#chan] <nick...>   every online clone sends MODE -o

%sTIPS%s
  • prefix any command with '/' or '.' if you have muscle memory from IRC
  • quote arguments with " " when they contain spaces (e.g. msg #foo "hi there")
  • to bypass the startup wizard pass -no-wizard or any of -n / -bind-v4 / -bind-v6

`, bold, reset, cyan, reset, cyan, reset, cyan, reset, cyan, reset)
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
