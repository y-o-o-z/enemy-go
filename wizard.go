package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// WizardChoice is the result of the interactive startup wizard.
type WizardChoice struct {
	Mode     IPMode
	BindV4   []string // explicit user-picked IPv4 binds (or all auto-detected)
	BindV6   []string // explicit user-picked IPv6 binds (or all auto-detected)
	Count    int
	Channels []string
}

// RunStartupWizard interactively asks the operator which IP family to use,
// which local IPs to bind from, how many clones to launch, and what
// channels to auto-join. The address family is *derived* from the IPs the
// operator picks, so they cannot end up with mode=ipv4 + only v6 binds.
func RunStartupWizard(in io.Reader, out io.Writer) (WizardChoice, error) {
	r := bufio.NewReader(in)
	pr := func(format string, args ...any) { fmt.Fprintf(out, format, args...) }

	v4, v6, err := DiscoverLocalIPs()
	if err != nil {
		return WizardChoice{}, fmt.Errorf("detect local IPs: %w", err)
	}

	pr("\n=== enemy-go interactive setup ===\n")
	pr("Detected local IPv4 addresses (%d):\n", len(v4))
	for i, ip := range v4 {
		pr("  v4[%d] %s\n", i+1, ip)
	}
	pr("Detected local IPv6 addresses (%d):\n", len(v6))
	for i, ip := range v6 {
		pr("  v6[%d] %s\n", i+1, ip)
	}
	if len(v4) == 0 && len(v6) == 0 {
		return WizardChoice{}, fmt.Errorf("no usable local IPs detected — check your interfaces")
	}

	// 1) Family selection.
	var mode IPMode
	for {
		pr("\nAddress family? [ipv4 / ipv6 / both] (default: both): ")
		ans, err := readLine(r)
		if err != nil {
			return WizardChoice{}, err
		}
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans == "" {
			ans = "both"
		}
		mode, err = ParseIPMode(ans)
		if err != nil {
			pr("  [!] %v — try again.\n", err)
			continue
		}
		if (mode == ModeV4 && len(v4) == 0) || (mode == ModeV6 && len(v6) == 0) {
			pr("  [!] no local %s addresses detected — pick another family.\n", mode)
			continue
		}
		break
	}

	// 2) Bind IP selection per family.
	var pickV4, pickV6 []string
	if mode != ModeV6 && len(v4) > 0 {
		pickV4, err = pickIPs(r, out, "IPv4", v4)
		if err != nil {
			return WizardChoice{}, err
		}
	}
	if mode != ModeV4 && len(v6) > 0 {
		pickV6, err = pickIPs(r, out, "IPv6", v6)
		if err != nil {
			return WizardChoice{}, err
		}
	}

	// Re-derive family based on what was actually picked. This protects
	// against e.g. user choosing "both" but then not picking any v6 IPs.
	switch {
	case len(pickV4) > 0 && len(pickV6) > 0:
		mode = ModeBoth
	case len(pickV4) > 0:
		mode = ModeV4
	case len(pickV6) > 0:
		mode = ModeV6
	default:
		return WizardChoice{}, fmt.Errorf("no bind IPs selected")
	}
	pr("[*] effective mode: %s (v4=%d v6=%d)\n", mode, len(pickV4), len(pickV6))

	// 3) Clone count.
	var count int
	for {
		pr("How many clones to spawn at startup? (default: 1): ")
		ans, err := readLine(r)
		if err != nil {
			return WizardChoice{}, err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" {
			count = 1
			break
		}
		n, err := strconv.Atoi(ans)
		if err != nil || n < 0 {
			pr("  [!] enter a non-negative integer.\n")
			continue
		}
		count = n
		break
	}

	// 4) Channels.
	pr("Channels to auto-join (comma-separated, blank = none): ")
	chLine, err := readLine(r)
	if err != nil {
		return WizardChoice{}, err
	}
	channels := splitCSV(chLine)

	return WizardChoice{
		Mode:     mode,
		BindV4:   pickV4,
		BindV6:   pickV6,
		Count:    count,
		Channels: channels,
	}, nil
}

// pickIPs lets the user pick a subset of available addresses or accept all.
// Accepted answers: "all" / "" → every IP; comma-separated indices ("1,3");
// or comma-separated literal IPs (validated against the available set).
func pickIPs(r *bufio.Reader, out io.Writer, family string, available []string) ([]string, error) {
	pr := func(format string, args ...any) { fmt.Fprintf(out, format, args...) }
	for {
		pr("\nWhich %s addresses to bind? (default: all)\n", family)
		pr("  - press Enter or type 'all' to use every detected %s address\n", family)
		pr("  - or list indices, e.g. '1,3'\n")
		pr("  - or list literal IPs, e.g. '203.0.113.5,203.0.113.7'\n")
		pr("> ")
		ans, err := readLine(r)
		if err != nil {
			return nil, err
		}
		ans = strings.TrimSpace(ans)
		if ans == "" || strings.EqualFold(ans, "all") {
			out := append([]string(nil), available...)
			return out, nil
		}
		toks := splitCSV(ans)
		picked := make([]string, 0, len(toks))
		seen := map[string]bool{}
		bad := false
		for _, t := range toks {
			// numeric index?
			if n, err := strconv.Atoi(t); err == nil {
				if n < 1 || n > len(available) {
					pr("  [!] index %d out of range\n", n)
					bad = true
					break
				}
				ip := available[n-1]
				if !seen[ip] {
					seen[ip] = true
					picked = append(picked, ip)
				}
				continue
			}
			// literal IP — validate and ensure it's in the available list
			if net.ParseIP(t) == nil {
				pr("  [!] %q is not a valid IP\n", t)
				bad = true
				break
			}
			matched := false
			for _, a := range available {
				if a == t {
					matched = true
					break
				}
			}
			if !matched {
				pr("  [!] %s not in detected %s pool — type 'pool' to inspect\n", t, family)
				bad = true
				break
			}
			if !seen[t] {
				seen[t] = true
				picked = append(picked, t)
			}
		}
		if bad || len(picked) == 0 {
			continue
		}
		return picked, nil
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// IsInteractiveStdin returns true when stdin looks like a TTY. Used to
// decide whether to default into the wizard.
func IsInteractiveStdin() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
