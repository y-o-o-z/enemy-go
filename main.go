package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var version = "1.0.0"

const banner = `
We are the
   ___  ____  ___  ____ ___  __  __
  / _ \/ __ \/ _ \/ __ ` + "`" + `__ \/ / / /
 /  __/ / / /  __/ / / / / / /_/ /
 \___/_/ /_/\___/_/ /_/ /_/\__, /
           you shall fear /____/

enemy-go v%s — IRCnet test-clones harness
`

func main() {
	var (
		modeStr     = flag.String("mode", "both", "address family: ipv4, ipv6, both")
		count       = flag.Int("n", 0, "number of clones to spawn at startup (0 = none, drop to shell)")
		serverURL   = flag.String("server-list", DefaultServerListURL, "IRCnet server registry JSON URL")
		serverCSV   = flag.String("servers", "", "comma-separated override list of server hostnames (skips registry fetch)")
		ircPort     = flag.Int("port", 6667, "IRC server port")
		bindV4CSV   = flag.String("bind-v4", "", "comma-separated local IPv4 addresses to bind (default: auto-detect)")
		bindV6CSV   = flag.String("bind-v6", "", "comma-separated local IPv6 addresses to bind (default: auto-detect)")
		channelsCSV = flag.String("channels", "", "comma-separated channels every clone should join")
		stagger     = flag.Duration("stagger", 250*time.Millisecond, "delay between successive connect attempts during spawn")
		realnames    = flag.String("realnames", "", "path to file with one realname per line (optional)")
		reasons      = flag.String("reasons", "", "path to file with one quit-reason per line (optional)")
		kickReasons  = flag.String("kick-reasons", "", "path to file with one kick-reason per line (optional)")
		showVersion = flag.Bool("version", false, "print version and exit")
		listOnly    = flag.Bool("list-servers", false, "fetch+print the open IRCnet server list, then exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, banner, version)
		fmt.Fprintf(os.Stderr, "\nUsage: %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("enemy-go %s\n", version)
		return
	}

	mode, err := ParseIPMode(*modeStr)
	if err != nil {
		log.Fatalf("invalid -mode: %v", err)
	}

	v4 := splitCSV(*bindV4CSV)
	v6 := splitCSV(*bindV6CSV)
	var pool *LocalIPPool
	if v4 == nil && v6 == nil {
		pool, err = NewLocalIPPool(nil, nil, mode)
	} else {
		pool, err = NewLocalIPPool(v4, v6, mode)
	}
	if err != nil {
		log.Fatalf("local IP pool: %v", err)
	}
	pV4, pV6 := pool.Counts()

	fmt.Printf(banner, version)
	fmt.Printf("[*] mode=%s  local IPs: v4=%d v6=%d\n", mode, pV4, pV6)

	// fetch server list
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var servers []IRCnetServer
	if csv := splitCSV(*serverCSV); len(csv) > 0 {
		for _, name := range csv {
			servers = append(servers, IRCnetServer{Name: name, Open: true})
		}
		fmt.Printf("[*] using %d server(s) from -servers flag\n", len(servers))
	} else {
		fmt.Printf("[*] fetching %s ...\n", *serverURL)
		servers, err = FetchIRCnetServers(ctx, *serverURL)
		if err != nil {
			log.Fatalf("fetch server list: %v", err)
		}
		fmt.Printf("[*] %d open IRCnet servers\n", len(servers))
	}
	servers = ResolveServers(ctx, servers, mode)
	v4Srv, v6Srv := PartitionByFamily(servers)
	fmt.Printf("[*] resolved: %d total (v4=%d, v6=%d)\n", len(servers), len(v4Srv), len(v6Srv))

	if *listOnly {
		for _, s := range servers {
			fmt.Println(FormatServerLine(s))
		}
		return
	}

	if len(servers) == 0 {
		log.Fatalf("no servers usable for mode=%s", mode)
	}

	mgr := NewManager(ManagerConfig{
		Pool:        pool,
		Servers:     servers,
		Mode:        mode,
		IRCPort:     *ircPort,
		JoinChans:   splitCSV(*channelsCSV),
		Realnames:   readLinesOr(*realnames, defaultRealnames),
		QuitReasons: readLinesOr(*reasons, defaultReasons),
		KickReasons: readLinesOr(*kickReasons, defaultKickReasons),
		Stagger:     *stagger,
		Log:         log.Printf,
	})

	if *count > 0 {
		fmt.Printf("[*] spawning %d clones (stagger=%s)...\n", *count, *stagger)
		if _, err := mgr.Spawn(*count); err != nil {
			log.Fatalf("spawn: %v", err)
		}
	}

	// trap signals so Ctrl-C tries a clean QUIT round-trip first.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Printf("\n[*] signal received, disconnecting clones...\n")
		shutCtx, c := context.WithTimeout(context.Background(), 8*time.Second)
		defer c()
		mgr.QuitAll(shutCtx)
		os.Exit(0)
	}()

	shell := NewShell(mgr, *serverURL)
	shell.Run()
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func readLinesOr(path string, fallback []string) []string {
	if path == "" {
		return fallback
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[warn] could not read %s: %v — using built-in defaults", path, err)
		return fallback
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

var defaultRealnames = []string{
	"john doe", "alex", "sam", "max", "leo", "kris", "marcin", "tomek",
	"piotrek", "michal", "kuba", "darek", "anonymous", "nobody", "ghost",
	"phantom", "void", "noone", "user", "guest",
}

// defaultReasons are sent as QUIT messages. They imitate generic IRC
// disconnect strings so the clones look like real users dropping.
var defaultReasons = []string{
	"bye",
	"see you",
	"leaving",
	"connection reset by peer",
	"client exited",
	"EOF from client",
	"Ping timeout",
	"Quit",
	"Read error: Connection reset by peer",
}

// defaultKickReasons are sent in the KICK command. The [PT] suffix is kept
// as a nod to the original (Pojeby Team) but the content is fresh.
var defaultKickReasons = []string{
	"End of transmission. [PT]",
	"Channel sanitized. [PT]",
	"Connection refused by ownership. [PT]",
	"Welcome to /dev/null. [PT]",
	"Compiled with hate, deployed with intent. [PT]",
	"You logged into the wrong network. [PT]",
	"Recompiled, redeployed, removed. [PT]",
	"Better luck on a different server. [PT]",
}
