package netsec

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

const fp = "SHA256:sharedkey"

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("bad prefix %q: %v", s, err)
	}
	return p
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad addr %q: %v", s, err)
	}
	return a
}

var testNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// 经典混权场景：条目 A 来源宽但强制命令 cmd-a，
// 条目 B 来源严但不限制命令。A 的来源 + B 的自由命令是更宽授权，必须拒绝。
func TestNoMixingAcrossEntries(t *testing.T) {
	entries := []Entry{
		{ID: "a", Fingerprint: fp, FromCIDRs: []netip.Prefix{mustPrefix(t, "10.0.0.0/8")}, Command: "cmd-a"},
		{ID: "b", Fingerprint: fp, FromCIDRs: []netip.Prefix{mustPrefix(t, "10.1.0.0/16")}},
	}
	// 来源 10.1.2.3 同时命中两条目；A 强制 cmd-a，B 不限制命令 —— 命令冲突。
	req := Request{Source: mustAddr(t, "10.1.2.3"), Command: "cmd-a", Now: testNow}

	if _, err := Evaluate(entries, fp, req, ""); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("auto select: want ErrCommandConflict, got %v", err)
	}
	d, err := Evaluate(entries, fp, req, "b")
	if err != nil {
		t.Fatalf("explicit pick b: %v", err)
	}
	if d.Evidence.ForcedCommand != "" || d.Evidence.MatchedCIDR != "10.1.0.0/16" {
		t.Fatalf("evidence not from entry b alone: %+v", d.Evidence)
	}
	// 混合请求：A 的来源范围 + B 的自由命令，但来源 10.2.x 不在 B 内 —— 必须拒绝。
	mixed := Request{Source: mustAddr(t, "10.2.3.4"), Command: "cmd-b", Now: testNow}
	if _, err := Evaluate(entries, fp, mixed, ""); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("mixed request must be denied, got %v", err)
	}
	if _, err := Evaluate(entries, fp, mixed, "a"); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("entry a does not permit mixed request, got %v", err)
	}
}

func TestAutoSelectSingleCandidate(t *testing.T) {
	entries := []Entry{
		{ID: "a", Fingerprint: fp, Command: "cmd-a", ValidBefore: testNow.Add(-time.Hour)}, // 已过期
		{ID: "b", Fingerprint: fp, Command: "cmd-b"},
		{ID: "c", Fingerprint: "SHA256:other", Command: "cmd-c"}, // 不同指纹，忽略
	}
	d, err := Evaluate(entries, fp, Request{Command: "cmd-b", Now: testNow}, "")
	if err != nil {
		t.Fatalf("single candidate should auto select: %v", err)
	}
	if d.Evidence.EntryID != "b" {
		t.Fatalf("want entry b, got %+v", d.Evidence)
	}
}

func TestAmbiguousWithoutConflictStillRefused(t *testing.T) {
	entries := []Entry{
		{ID: "a", Fingerprint: fp, Command: "same"},
		{ID: "b", Fingerprint: fp, Command: "same"},
	}
	_, err := Evaluate(entries, fp, Request{Command: "same", Now: testNow}, "")
	if !errors.Is(err, ErrAmbiguous) || errors.Is(err, ErrCommandConflict) {
		t.Fatalf("want plain ErrAmbiguous, got %v", err)
	}
	d, err := Evaluate(entries, fp, Request{Command: "same", Now: testNow}, "b")
	if err != nil || d.Evidence.EntryID != "b" {
		t.Fatalf("explicit pick failed: %v %+v", err, d)
	}
}

func TestConjunctionWithinEntry(t *testing.T) {
	e := Entry{
		ID:          "x",
		Fingerprint: fp,
		FromCIDRs:   []netip.Prefix{mustPrefix(t, "192.0.2.0/24")},
		Command:     "backup",
		PermitOpen:  []string{"db:5432"},
		ValidAfter:  testNow.Add(-time.Hour),
		ValidBefore: testNow.Add(time.Hour),
	}
	good := Request{Source: mustAddr(t, "192.0.2.9"), Command: "backup", Target: "db:5432", Now: testNow}
	if _, ok := e.Allows(good); !ok {
		t.Fatal("conjunction should pass")
	}
	bads := []Request{
		{Source: mustAddr(t, "198.51.100.1"), Command: "backup", Target: "db:5432", Now: testNow},                 // 来源不符
		{Source: mustAddr(t, "192.0.2.9"), Command: "shell", Target: "db:5432", Now: testNow},                     // 命令不符
		{Source: mustAddr(t, "192.0.2.9"), Command: "backup", Target: "evil:22", Now: testNow},                    // 目标不符
		{Source: mustAddr(t, "192.0.2.9"), Command: "backup", Target: "db:5432", Now: testNow.Add(2 * time.Hour)}, // 过期
	}
	for i, r := range bads {
		if _, ok := e.Allows(r); ok {
			t.Fatalf("case %d should be denied", i)
		}
	}
}
