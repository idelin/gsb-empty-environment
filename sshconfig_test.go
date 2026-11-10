package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func parse(t *testing.T, dir, content string) *Config {
	t.Helper()
	cfg, err := ParseFile(writeFile(t, dir, "ssh_config", content))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHostWildcardAndNegation(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host *.internal !bastion.internal
  Port 2222

Host db-?
  Port 3306
`)
	if _, ok := cfg.Get("web.internal", "port"); !ok {
		t.Fatal("web.internal should match *.internal")
	}
	if v, _ := cfg.Get("web.internal", "port"); v != "2222" {
		t.Fatalf("port = %q, want 2222", v)
	}
	if _, ok := cfg.Get("bastion.internal", "port"); ok {
		t.Fatal("negated pattern must exclude bastion.internal")
	}
	if v, ok := cfg.Get("db-1", "port"); !ok || v != "3306" {
		t.Fatalf("db-1 should match db-?, got %q", v)
	}
	if _, ok := cfg.Get("db-12", "port"); ok {
		t.Fatal("? must match exactly one character")
	}
}

func TestFirstValueWins(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host app
  Port 1000
  HostName first.example

Host app *
  Port 2000
  User deploy

Host *
  HostName fallback.example
  Port 22
`)
	if v, _ := cfg.Get("app", "port"); v != "1000" {
		t.Fatalf("port = %q, want first value 1000", v)
	}
	if v, _ := cfg.Get("app", "hostname"); v != "first.example" {
		t.Fatalf("hostname = %q, want first.example", v)
	}
	if v, _ := cfg.Get("app", "user"); v != "deploy" {
		t.Fatalf("user = %q, want deploy from first block that sets it", v)
	}
	if v, _ := cfg.Get("other", "port"); v != "2000" {
		t.Fatalf("other port = %q, want 2000 from 'Host app *'", v)
	}
	if v, _ := cfg.Get("other", "hostname"); v != "fallback.example" {
		t.Fatalf("other hostname = %q, want fallback.example", v)
	}
}

func TestStaticIncludeSpliceOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "included.conf", `
Host inc
  Port 1111
`)
	main := writeFile(t, dir, "ssh_config", `
Host before
  Port 1

Include included.conf

Host after
  Port 2
`)
	cfg, err := ParseFile(main)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, b := range cfg.Blocks {
		if v, ok := func() (string, bool) {
			for _, kv := range b.KVs {
				if kv.Key == "port" {
					return kv.Value, true
				}
			}
			return "", false
		}(); ok {
			order = append(order, v)
		}
	}
	if !reflect.DeepEqual(order, []string{"1", "1111", "2"}) {
		t.Fatalf("block order = %v, want [1 1111 2]", order)
	}
}

func TestIncludeCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.conf", "Include b.conf\n")
	writeFile(t, dir, "b.conf", "Include a.conf\n")
	_, err := ParseFile(filepath.Join(dir, "a.conf"))
	var ice *IncludeCycleError
	if !errors.As(err, &ice) {
		t.Fatalf("err = %v, want IncludeCycleError", err)
	}
	var jle *JumpLoopError
	if errors.As(err, &jle) {
		t.Fatal("include cycle must not be reported as jump loop")
	}
}

func TestProxyJumpPerHopReevaluation(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host target
  HostName 10.0.0.5
  ProxyJump jump1,ops@jump2:2222

Host jump1
  HostName 10.0.0.1
  Port 2201
  ProxyJump relay

Host relay
  HostName 10.0.0.254

Host jump2
  HostName 10.0.0.2
  Port 22
`)
	chain, err := Resolve(cfg, "target")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(chain.Hops))
	for _, h := range chain.Hops {
		got = append(got, h.Alias+"="+h.Endpoint())
	}
	want := []string{
		"target=10.0.0.5:22",
		"jump1=10.0.0.1:2201",
		"relay=10.0.0.254:22", // jump1's own ProxyJump, re-evaluated by alias
		"jump2=10.0.0.2:2222", // explicit port in hop spec overrides config
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	if chain.Hops[3].User != "ops" {
		t.Fatalf("jump2 user = %q, want ops", chain.Hops[3].User)
	}
}

func TestProxyJumpNone(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host solo
  ProxyJump none
`)
	chain, err := Resolve(cfg, "solo")
	if err != nil {
		t.Fatal(err)
	}
	if len(chain.Hops) != 1 {
		t.Fatalf("hops = %d, want 1", len(chain.Hops))
	}
}

func TestJumpLoopDistinctFromIncludeCycle(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host a
  ProxyJump b

Host b
  ProxyJump a
`)
	_, err := Resolve(cfg, "a")
	var jle *JumpLoopError
	if !errors.As(err, &jle) {
		t.Fatalf("err = %v, want JumpLoopError", err)
	}
	var ice *IncludeCycleError
	if errors.As(err, &ice) {
		t.Fatal("jump loop must not be reported as include cycle")
	}
	if want := []string{"a", "b", "a"}; !reflect.DeepEqual(jle.Chain, want) {
		t.Fatalf("loop chain = %v, want %v", jle.Chain, want)
	}
}

func TestForbiddenMarking(t *testing.T) {
	cfg := parse(t, t.TempDir(), `
Host target
  HostName 10.0.0.5
  ProxyJump bastion

Host bastion
  HostName 203.0.113.1
  Port 2222
`)
	chain, err := Resolve(cfg, "target")
	if err != nil {
		t.Fatal(err)
	}
	forbidden, _ := ParseForbiddenSet("203.0.113.1:2222, 192.0.2.1:22")
	if !chain.MarkForbidden(forbidden) {
		t.Fatal("expected violation")
	}
	if chain.Hops[0].Forbidden {
		t.Fatal("target hop must not be flagged")
	}
	if !chain.Hops[1].Forbidden {
		t.Fatal("bastion hop must be flagged")
	}
}

// TestSameConfigDifferentAliases verifies that one config (including a
// shared Include file) resolves consistently for different aliases that
// match the same wildcard block.
func TestSameConfigDifferentAliases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "common.conf", `
Host bastion
  HostName 203.0.113.1
  Port 2222
`)
	main := writeFile(t, dir, "ssh_config", `
Include common.conf

Host web-*
  ProxyJump bastion
  Port 22
`)
	cfg, err := ParseFile(main)
	if err != nil {
		t.Fatal(err)
	}

	resolveEndpoints := func(alias string) []string {
		chain, err := Resolve(cfg, alias)
		if err != nil {
			t.Fatal(err)
		}
		var eps []string
		for _, h := range chain.Hops {
			eps = append(eps, h.Endpoint())
		}
		return eps
	}

	got1 := resolveEndpoints("web-1")
	got2 := resolveEndpoints("web-2")
	want1 := []string{"web-1:22", "203.0.113.1:2222"}
	want2 := []string{"web-2:22", "203.0.113.1:2222"}
	if !reflect.DeepEqual(got1, want1) {
		t.Fatalf("web-1 chain = %v, want %v", got1, want1)
	}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("web-2 chain = %v, want %v", got2, want2)
	}

	// The shared jump host is identical for both aliases, so a single
	// forbidden entry flags both chains.
	forbidden, _ := ParseForbiddenSet("203.0.113.1:2222")
	for _, alias := range []string{"web-1", "web-2"} {
		chain, err := Resolve(cfg, alias)
		if err != nil {
			t.Fatal(err)
		}
		if !chain.MarkForbidden(forbidden) {
			t.Fatalf("alias %s should violate via shared bastion", alias)
		}
	}
}
