package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultServerListURL is the IRCnet "by country" server registry endpoint
// discovered from the ircnet.info SPA bundle. The SPA itself renders no
// server-side HTML, so we hit the JSON API directly.
const DefaultServerListURL = "https://bot.ircnet.info/api/v2/serversByCountry"

// IRCnetServer represents one IRCnet server entry.
type IRCnetServer struct {
	Name      string `json:"serverName"`
	SID       string `json:"sid"`
	Info      string `json:"serverInfo"`
	UserCount int    `json:"userCount"`
	Version   string `json:"version"`
	Open      bool   `json:"open"`
	SASL      bool   `json:"sasl"`
	LastSeen  string `json:"lastSeen"`

	Country string `json:"-"` // alpha-2 country code (filled from parent group)

	// Resolved A/AAAA addresses (filled by Resolve).
	V4 []string `json:"-"`
	V6 []string `json:"-"`
}

type serverByCountryResp struct {
	Countries []struct {
		CountryCodeAlpha2 string         `json:"countryCodeAlpha2"`
		ServerList        []IRCnetServer `json:"serverList"`
	} `json:"countriesWithServers"`
}

// FetchIRCnetServers calls the registry and returns only servers marked
// "open" (i.e. accepting client connections).
func FetchIRCnetServers(ctx context.Context, url string) ([]IRCnetServer, error) {
	if url == "" {
		url = DefaultServerListURL
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "enemy-go/1.0 (+github.com/kofany/enemy-go)")
	req.Header.Set("Accept", "application/json")

	cl := &http.Client{Timeout: 20 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server list HTTP %d", resp.StatusCode)
	}
	var raw serverByCountryResp
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var out []IRCnetServer
	for _, c := range raw.Countries {
		for _, s := range c.ServerList {
			if !s.Open {
				continue
			}
			s.Country = c.CountryCodeAlpha2
			out = append(out, s)
		}
	}
	// stable order: by user count desc, then name
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserCount != out[j].UserCount {
			return out[i].UserCount > out[j].UserCount
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ResolveServers performs A/AAAA lookups for each server in parallel and
// returns only servers that have at least one address in the requested
// family/families.
func ResolveServers(ctx context.Context, servers []IRCnetServer, mode IPMode) []IRCnetServer {
	out := make([]IRCnetServer, len(servers))
	var wg sync.WaitGroup
	r := &net.Resolver{}
	sem := make(chan struct{}, 16)
	for i := range servers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s := servers[i]
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			ips, err := r.LookupIPAddr(rctx, s.Name)
			if err == nil {
				for _, a := range ips {
					if a.IP.To4() != nil {
						s.V4 = append(s.V4, a.IP.String())
					} else {
						s.V6 = append(s.V6, a.IP.String())
					}
				}
			}
			out[i] = s
		}(i)
	}
	wg.Wait()

	filtered := out[:0]
	for _, s := range out {
		switch mode {
		case ModeV4:
			if len(s.V4) > 0 {
				filtered = append(filtered, s)
			}
		case ModeV6:
			if len(s.V6) > 0 {
				filtered = append(filtered, s)
			}
		default:
			if len(s.V4) > 0 || len(s.V6) > 0 {
				filtered = append(filtered, s)
			}
		}
	}
	return filtered
}

// PartitionByFamily splits resolved servers into "has IPv4" and "has IPv6"
// buckets. A server may appear in both.
func PartitionByFamily(servers []IRCnetServer) (v4, v6 []IRCnetServer) {
	for _, s := range servers {
		if len(s.V4) > 0 {
			v4 = append(v4, s)
		}
		if len(s.V6) > 0 {
			v6 = append(v6, s)
		}
	}
	return
}

// FormatServerLine renders a one-line summary suitable for status output.
func FormatServerLine(s IRCnetServer) string {
	fams := []string{}
	if len(s.V4) > 0 {
		fams = append(fams, "v4")
	}
	if len(s.V6) > 0 {
		fams = append(fams, "v6")
	}
	return fmt.Sprintf("%-2s %-35s users=%-5d %s", s.Country, s.Name, s.UserCount, strings.Join(fams, "+"))
}
