package main

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/KnifeLemon/Droidline/server/internal/store"
)

// portRange is one inclusive range of ports the OS keeps for itself.
type portRange struct{ start, end int }

func (r portRange) String() string {
	if r.start == r.end {
		return strconv.Itoa(r.start)
	}
	return fmt.Sprintf("%d-%d", r.start, r.end)
}

var rangeLine = regexp.MustCompile(`^\s*(\d+)\s+(\d+)`)

// parseExcluded reads `netsh interface ipv4 show excludedportrange` output. Only the number columns are used,
// so a localized Windows gives the same result.
func parseExcluded(out string) []portRange {
	var rs []portRange
	for _, line := range strings.Split(out, "\n") {
		m := rangeLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a > 0 && b >= a {
			rs = append(rs, portRange{a, b})
		}
	}
	return rs
}

func reservedBy(rs []portRange, port int) (portRange, bool) {
	for _, r := range rs {
		if port >= r.start && port <= r.end {
			return r, true
		}
	}
	return portRange{}, false
}

// listenPort returns the port of a listen address such as ":8779" or "127.0.0.1:8780".
func listenPort(listen string) (int, bool) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(p)
	return n, err == nil && n > 0
}

// freePortAfter suggests a port above from that no reserved range covers, skipping ports in taken.
func freePortAfter(from int, rs []portRange, taken map[int]bool) int {
	for p := from + 1; p < 65536; p++ {
		if r, ok := reservedBy(rs, p); ok {
			p = r.end
			continue
		}
		if !taken[p] {
			return p
		}
	}
	return 0
}

// checkReserved reports, on Windows, whether the agent, client or discovery port sits in a range Windows keeps
// for itself. A port there cannot be opened, though nothing else is using it. It returns the sections that hit one.
func checkReserved(cfg store.Config, check func(ok bool, label, hint string)) map[string]bool {
	tcp, err := excludedRanges("tcp")
	if err != nil {
		return map[string]bool{}
	}
	udp, _ := excludedRanges("udp")
	return checkReservedWith(cfg, tcp, udp, check)
}

func checkReservedWith(cfg store.Config, tcp, udp []portRange, check func(ok bool, label, hint string)) map[string]bool {
	hit := map[string]bool{}
	type entry struct {
		section, listen, proto string
		rs                     []portRange
	}
	entries := []entry{{"agent", cfg.Agent.Listen, "tcp", tcp}, {"client", cfg.Client.Listen, "tcp", tcp}}
	if cfg.Discovery.Listen != "off" {
		entries = append(entries, entry{"discovery", cfg.Discovery.Listen, "udp", udp})
	}
	taken := map[int]bool{}
	for _, e := range entries {
		if p, ok := listenPort(e.listen); ok {
			taken[p] = true
		}
	}
	all := append(append([]portRange{}, tcp...), udp...)
	for _, e := range entries {
		p, ok := listenPort(e.listen)
		if !ok {
			continue
		}
		label := fmt.Sprintf("%s port %d (%s) is not reserved by Windows", e.section, p, e.proto)
		r, bad := reservedBy(e.rs, p)
		if !bad {
			check(true, label, "")
			continue
		}
		hit[e.section] = true
		label = fmt.Sprintf("%s port %d (%s) is reserved by Windows", e.section, p, e.proto)
		alt := freePortAfter(r.end, all, taken)
		taken[alt] = true
		host, _, _ := net.SplitHostPort(e.listen)
		hint := fmt.Sprintf("Windows keeps %s %s for itself (often for Hyper-V, WSL or Docker), so the port cannot be opened.\n"+
			"       Set listen = %q under [%s] in config.toml, then restart droidline serve.",
			e.proto, r, net.JoinHostPort(host, fmt.Sprint(alt)), e.section)
		if e.section == "client" {
			hint += fmt.Sprintf("\n       The droidline commands follow it. Scripts using an SDK need DROIDLINE_PORT=%d.", alt)
		}
		check(false, label, hint)
	}
	return hit
}
