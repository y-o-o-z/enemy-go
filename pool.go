package main

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// IPMode controls which address family clones bind to.
type IPMode int

const (
	ModeBoth IPMode = iota
	ModeV4
	ModeV6
)

func ParseIPMode(s string) (IPMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "both", "dual", "any":
		return ModeBoth, nil
	case "ipv4", "v4", "4":
		return ModeV4, nil
	case "ipv6", "v6", "6":
		return ModeV6, nil
	}
	return 0, fmt.Errorf("invalid ip mode %q (use ipv4, ipv6, both)", s)
}

func (m IPMode) String() string {
	switch m {
	case ModeV4:
		return "ipv4"
	case ModeV6:
		return "ipv6"
	default:
		return "both"
	}
}

// LocalIPPool holds the set of local IPs used as bind sources, partitioned
// by family. Pick() does round-robin selection.
type LocalIPPool struct {
	v4 []string
	v6 []string

	mu sync.Mutex
	i4 atomic.Uint64
	i6 atomic.Uint64
}

// DiscoverLocalIPs returns globally-routable IPv4/IPv6 addresses on host
// interfaces. Loopback, link-local, multicast, unspecified addresses and
// IPv6 ULAs (fc00::/7) are excluded.
func DiscoverLocalIPs() (v4, v6 []string, err error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, nil, err
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
				ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			if ip.To4() != nil {
				v4 = append(v4, ip.To4().String())
			} else {
				if ip.IsPrivate() { // IPv6 ULA
					continue
				}
				v6 = append(v6, ip.String())
			}
		}
	}
	return v4, v6, nil
}

// NewLocalIPPool builds a pool from explicit lists or, if both are nil,
// auto-discovers them. Mode filters which families are kept.
func NewLocalIPPool(v4, v6 []string, mode IPMode) (*LocalIPPool, error) {
	if v4 == nil && v6 == nil {
		dv4, dv6, err := DiscoverLocalIPs()
		if err != nil {
			return nil, err
		}
		v4, v6 = dv4, dv6
	}
	p := &LocalIPPool{}
	if mode != ModeV6 {
		p.v4 = filterValidIPs(v4, true)
	}
	if mode != ModeV4 {
		p.v6 = filterValidIPs(v6, false)
	}
	if len(p.v4) == 0 && len(p.v6) == 0 {
		return nil, fmt.Errorf("no usable local IPs found for mode=%s", mode)
	}
	return p, nil
}

func filterValidIPs(ips []string, wantV4 bool) []string {
	out := make([]string, 0, len(ips))
	seen := map[string]bool{}
	for _, s := range ips {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		isV4 := ip.To4() != nil
		if wantV4 != isV4 {
			continue
		}
		key := ip.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// PickV4 returns the next IPv4 address (round-robin), or "" if none.
func (p *LocalIPPool) PickV4() string {
	if len(p.v4) == 0 {
		return ""
	}
	i := p.i4.Add(1) - 1
	return p.v4[int(i%uint64(len(p.v4)))]
}

// PickV6 returns the next IPv6 address (round-robin), or "" if none.
func (p *LocalIPPool) PickV6() string {
	if len(p.v6) == 0 {
		return ""
	}
	i := p.i6.Add(1) - 1
	return p.v6[int(i%uint64(len(p.v6)))]
}

func (p *LocalIPPool) HasV4() bool { return len(p.v4) > 0 }
func (p *LocalIPPool) HasV6() bool { return len(p.v6) > 0 }

func (p *LocalIPPool) Counts() (int, int) {
	return len(p.v4), len(p.v6)
}

func (p *LocalIPPool) Snapshot() (v4, v6 []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v4 = append(v4, p.v4...)
	v6 = append(v6, p.v6...)
	return
}
