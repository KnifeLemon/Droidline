package main

import (
	"strings"
	"testing"

	"github.com/KnifeLemon/Droidline/server/internal/store"
)

const netshEN = `
Protocol tcp Port Exclusion Ranges

Start Port    End Port
----------    --------
      5357        5357
      8716        8815
     50000       50059     *

* - Administered port exclusions.
`

const netshKO = `
프로토콜 tcp 포트 제외 범위

시작 포트    끝 포트
----------    --------
      8716        8815
     50000       50059     *

* - 관리되는 포트 제외입니다.
`

func TestParseExcluded(t *testing.T) {
	for name, out := range map[string]string{"en": netshEN, "ko": netshKO} {
		rs := parseExcluded(strings.ReplaceAll(out, "\n", "\r\n"))
		if _, ok := reservedBy(rs, 8779); !ok {
			t.Errorf("%s: 8779 should be reserved, ranges %v", name, rs)
		}
		if r, ok := reservedBy(rs, 50059); !ok || r.String() != "50000-50059" {
			t.Errorf("%s: 50059 in %v", name, r)
		}
		if _, ok := reservedBy(rs, 11223); ok {
			t.Errorf("%s: 11223 should be free", name)
		}
	}
	if len(parseExcluded("")) != 0 {
		t.Error("empty output should give no ranges")
	}
}

func TestFreePortAfterSkipsRangesAndTaken(t *testing.T) {
	rs := []portRange{{8716, 8815}, {8816, 8820}}
	taken := map[int]bool{8821: true}
	if got := freePortAfter(8815, rs, taken); got != 8822 {
		t.Fatalf("got %d, want 8822", got)
	}
}

func TestListenPort(t *testing.T) {
	for in, want := range map[string]int{":8779": 8779, "127.0.0.1:8780": 8780, "[::]:11223": 11223} {
		if got, ok := listenPort(in); !ok || got != want {
			t.Errorf("%q: got %d %v", in, got, ok)
		}
	}
	if _, ok := listenPort("off"); ok {
		t.Error(`"off" has no port`)
	}
}

func TestCheckReservedReportsAndSuggests(t *testing.T) {
	rs := parseExcluded(netshEN)
	var cfg store.Config
	cfg.Agent.Listen, cfg.Client.Listen, cfg.Discovery.Listen = ":8779", "127.0.0.1:8780", ":8778"
	type line struct {
		ok          bool
		label, hint string
	}
	var got []line
	hit := checkReservedWith(cfg, rs, rs, func(ok bool, label, hint string) { got = append(got, line{ok, label, hint}) })
	if !hit["agent"] || !hit["client"] || !hit["discovery"] {
		t.Fatalf("hit %v", hit)
	}
	if len(got) != 3 || got[0].ok || !strings.Contains(got[0].hint, `":8816"`) || !strings.Contains(got[1].hint, `"127.0.0.1:8817"`) {
		t.Fatalf("lines %+v", got)
	}
	if !strings.Contains(got[1].hint, "DROIDLINE_PORT=8817") || !strings.Contains(got[2].hint, `":8818"`) {
		t.Fatalf("client or discovery hint %+v", got[1:])
	}
}
