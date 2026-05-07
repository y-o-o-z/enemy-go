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
		interactive = flag.Bool("i", false, "force interactive setup wizard (asks for family, bind IPs, count, channels)")
		noWizard    = flag.Bool("no-wizard", false, "disable the auto wizard even on a TTY (use raw flags only)")
		oidentMode  = flag.String("oident", "auto", "oidentd integration: auto (use if oidentd is reachable), on (require), off")
		oidentConf  = flag.String("oident-conf", "~/.oidentd.conf", "path to the per-user oidentd config file")
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

	fmt.Printf(banner, version)

	// Auto-trigger the wizard when no clone count is given AND no explicit
	// IP binds were passed AND stdin is a real TTY. -i forces it; -no-wizard
	// suppresses it.
	wantWizard := *interactive
	if !wantWizard && !*noWizard && *count == 0 && len(v4) == 0 && len(v6) == 0 && IsInteractiveStdin() && !*listOnly {
		wantWizard = true
	}

	if wantWizard {
		choice, werr := RunStartupWizard(os.Stdin, os.Stdout)
		if werr != nil {
			log.Fatalf("wizard: %v", werr)
		}
		mode = choice.Mode
		v4 = choice.BindV4
		v6 = choice.BindV6
		*count = choice.Count
		if len(choice.Channels) > 0 {
			*channelsCSV = strings.Join(choice.Channels, ",")
		}
	}

	var pool *LocalIPPool
	if v4 == nil && v6 == nil {
		pool, err = NewLocalIPPool(nil, nil, mode)
	} else {
		// Auto-derive family from explicitly-supplied binds. If the user
		// only gave IPv4 binds we lock the mode to v4 (and vice-versa);
		// if they gave both, we use 'both' regardless of the -mode flag.
		mode = deriveMode(v4, v6, mode)
		pool, err = NewLocalIPPool(v4, v6, mode)
	}
	if err != nil {
		log.Fatalf("local IP pool: %v", err)
	}
	pV4, pV6 := pool.Counts()

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

	oident, err := setupOident(*oidentMode, *oidentConf)
	if err != nil {
		log.Fatalf("oidentd: %v", err)
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
		Oident:      oident,
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
		_ = oident.Close()
		os.Exit(0)
	}()

	shell := NewShell(mgr, *serverURL)
	shell.Run()
	_ = oident.Close()
}

// setupOident parses the -oident mode and returns a manager (or nil when
// the integration is disabled). "auto" probes for a running oidentd and
// silently disables the feature if none is found; "on" hard-fails when
// oidentd is unreachable; "off" returns nil unconditionally.
func setupOident(mode, confPath string) (*OidentManager, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off", "no", "disable", "disabled":
		return nil, nil
	case "on", "force", "require", "required":
		if !DetectOidentd() {
			return nil, fmt.Errorf("-oident=on but oidentd is not reachable on 127.0.0.1:113")
		}
		return openOident(confPath)
	case "", "auto":
		if !DetectOidentd() {
			fmt.Printf("[*] oidentd: not detected — using static idents (set -oident=on to require)\n")
			return nil, nil
		}
		return openOident(confPath)
	}
	return nil, fmt.Errorf("invalid -oident value %q (use auto, on, off)", mode)
}

func openOident(path string) (*OidentManager, error) {
	m, err := NewOidentManager(path, log.Printf)
	if err != nil {
		return nil, err
	}
	fmt.Printf("[*] oidentd: enabled, managing %s\n", m.Path())
	return m, nil
}

// deriveMode adjusts the effective IP mode based on the bind lists the user
// actually supplied. Explicit binds win over the -mode flag: passing only
// IPv4 IPs forces v4-only, mixing both forces dual-stack, etc.
func deriveMode(v4, v6 []string, requested IPMode) IPMode {
	switch {
	case len(v4) > 0 && len(v6) > 0:
		return ModeBoth
	case len(v4) > 0:
		return ModeV4
	case len(v6) > 0:
		return ModeV6
	default:
		return requested
	}
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

// defaultKickReasons are sent in the KICK command. Refreshed list — no
// "Pojeby Team" / [PT] tag (legacy reference removed).
var defaultKickReasons = []string{
	"End of transmission.",
	"Channel sanitized.",
	"Connection refused by ownership.",
	"Welcome to /dev/null.",
	"Compiled with intent, deployed without remorse.",
	"You logged into the wrong network.",
	"Recompiled, redeployed, removed.",
	"Better luck on a different server.",
	"goodbye and thanks for all the fish.",
	"connection terminated by upstream policy.",
	"manual override engaged.",
	"buffer overflow detected, flushing.",
	"out of bounds — see you later.",
	"channel cleanup in progress.",
	"return to sender.",
	"this incident has been logged.",
}
