package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mod(name, digest string) ModuleID {
	return ModuleID{Name: name, Digest: digest}
}

func ref(m ModuleID, symbol string) Ref {
	return Ref{Module: m, Symbol: symbol}
}

func concrete(addr Address) Binding {
	return Binding{Concrete: true, Addr: addr}
}

func forward(r Ref) Binding {
	return Binding{To: r}
}

func mustCommit(t *testing.T, tx *Tx) {
	t.Helper()
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
}

func wantErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

// TestSameNameDifferentDigest proves module identity is bound to the digest:
// two builds of the same module name are distinct authorities, and a binding
// targeting one digest cannot be satisfied by the other.
func TestSameNameDifferentDigest(t *testing.T) {
	loader := NewLoader()
	libV1 := mod("lib", "sha256:v1")
	libV2 := mod("lib", "sha256:v2")

	tx := loader.Begin()
	if err := tx.AddModule(libV1, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.AddModule(libV2, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(libV1, "Open", concrete(0x1000)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(libV2, "Open", concrete(0x2000)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)

	got1, err := loader.Resolve(ref(libV1, "Open"))
	if err != nil {
		t.Fatalf("resolve v1: %v", err)
	}
	got2, err := loader.Resolve(ref(libV2, "Open"))
	if err != nil {
		t.Fatalf("resolve v2: %v", err)
	}
	if got1.Addr != 0x1000 || got2.Addr != 0x2000 {
		t.Fatalf("digest identities mixed up: v1=%#x v2=%#x", got1.Addr, got2.Addr)
	}
	if len(got1.Trail) != 1 || got1.Trail[0] != libV1 {
		t.Fatalf("unexpected v1 trail: %+v", got1.Trail)
	}

	// An unregistered third digest of the same name is simply unknown.
	rogue := mod("lib", "sha256:rogue")
	if _, err := loader.Resolve(ref(rogue, "Open")); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("want ErrUnknownModule for rogue digest, got %v", err)
	}

	// Re-adding an existing identity with conflicting authorization is rejected.
	tx = loader.Begin()
	wantErrIs(t, tx.AddModule(libV1, false), ErrModuleExists)
	tx.Discard()
}

// TestFullAuthorizationChain drives a multi-hop forwarding chain and confirms
// every hop, including the final concrete target, must be authorized. It also
// exercises update transactions that revoke and restore each hop.
func TestFullAuthorizationChain(t *testing.T) {
	loader := NewLoader()
	root := mod("root", "sha256:r")
	gateway := mod("gateway", "sha256:g")
	plugin := mod("plugin", "sha256:p")
	impl := mod("impl", "sha256:i")

	tx := loader.Begin()
	for _, m := range []ModuleID{root, gateway, plugin, impl} {
		if err := tx.AddModule(m, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Bind(root, "entry", forward(ref(gateway, "mid"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(gateway, "mid", forward(ref(plugin, "next"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(plugin, "next", forward(ref(impl, "do"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(impl, "do", concrete(0x4000)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)

	got, err := loader.Resolve(ref(root, "entry"))
	if err != nil {
		t.Fatalf("authorized chain: %v", err)
	}
	if got.Addr != 0x4000 {
		t.Fatalf("want final addr 0x4000, got %#x", got.Addr)
	}
	wantTrail := []ModuleID{root, gateway, plugin, impl}
	if len(got.Trail) != len(wantTrail) {
		t.Fatalf("trail length = %d, want %d (%+v)", len(got.Trail), len(wantTrail), got.Trail)
	}
	for i := range wantTrail {
		if got.Trail[i] != wantTrail[i] {
			t.Fatalf("trail[%d] = %v, want %v", i, got.Trail[i], wantTrail[i])
		}
	}

	// Revoke each module in turn; every position on the chain must be enforced.
	for _, revoked := range []ModuleID{root, gateway, plugin, impl} {
		tx = loader.Begin()
		if err := tx.SetAuthorized(revoked, false); err != nil {
			t.Fatal(err)
		}
		mustCommit(t, tx)

		_, err = loader.Resolve(ref(root, "entry"))
		wantErrIs(t, err, ErrUnauthorized)

		var ne *Error
		if !errors.As(err, &ne) {
			t.Fatalf("authorization failure lost identity context: %v", err)
		}
		if ne.Ref.Module != revoked {
			t.Fatalf("revoked %v but error points at %v", revoked, ne.Ref.Module)
		}

		// Restore before revoking the next hop.
		tx = loader.Begin()
		if err := tx.SetAuthorized(revoked, true); err != nil {
			t.Fatal(err)
		}
		mustCommit(t, tx)
		if _, err = loader.Resolve(ref(root, "entry")); err != nil {
			t.Fatalf("chain should recover after restoring %v: %v", revoked, err)
		}
	}
}

// TestSharedForwardChainCache points several entry symbols at the same
// downstream chain and verifies repeated resolution shares the per-generation
// cache instead of re-walking every hop.
func TestSharedForwardChainCache(t *testing.T) {
	loader := NewLoader()
	appA := mod("app-a", "sha256:a")
	appB := mod("app-b", "sha256:b")
	appC := mod("app-c", "sha256:c")
	core := mod("core", "sha256:c0")
	kernel := mod("kernel", "sha256:k")

	tx := loader.Begin()
	for _, m := range []ModuleID{appA, appB, appC, core, kernel} {
		if err := tx.AddModule(m, true); err != nil {
			t.Fatal(err)
		}
	}
	target := ref(kernel, "syscall")
	if err := tx.Bind(kernel, "syscall", concrete(0x7000)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(core, "call", forward(target)); err != nil {
		t.Fatal(err)
	}
	// Four independent entry points converge on the same shared chain.
	for _, entry := range []struct {
		m      ModuleID
		symbol string
	}{
		{appA, "a1"}, {appA, "a2"}, {appB, "b1"}, {appC, "c1"},
	} {
		if err := tx.Bind(entry.m, entry.symbol, forward(ref(core, "call"))); err != nil {
			t.Fatal(err)
		}
	}
	mustCommit(t, tx)

	entries := []Ref{
		ref(appA, "a1"), ref(appA, "a2"), ref(appB, "b1"), ref(appC, "c1"),
	}
	for i := 0; i < 3; i++ {
		for _, e := range entries {
			got, err := loader.Resolve(e)
			if err != nil {
				t.Fatalf("resolve %s: %v", e, err)
			}
			if got.Addr != 0x7000 {
				t.Fatalf("%s resolved to %#x", e, got.Addr)
			}
		}
	}

	cachedEntries, lookups := loader.cacheStats()
	// Distinct references ever walked: 4 entries + core call + kernel target.
	wantEntries := 6
	if cachedEntries != wantEntries {
		t.Fatalf("cache entries = %d, want %d", cachedEntries, wantEntries)
	}
	// Twelve resolutions, but each binding is fetched at most once.
	if lookups != wantEntries {
		t.Fatalf("binding lookups = %d, want %d (cache not shared)", lookups, wantEntries)
	}

	// A cached failure keeps returning the original verdict.
	if _, err := loader.Resolve(ref(kernel, "missing")); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("want ErrUnknownSymbol, got %v", err)
	}
	if _, err := loader.Resolve(ref(kernel, "missing")); !errors.Is(err, ErrUnknownSymbol) {
		t.Fatalf("cached failure changed verdict: %v", err)
	}
}

// TestCycleRejected covers self-loops and multi-module cycles.
func TestCycleRejected(t *testing.T) {
	loader := NewLoader()
	alpha := mod("alpha", "sha256:a")
	beta := mod("beta", "sha256:b")
	gamma := mod("gamma", "sha256:g")

	tx := loader.Begin()
	for _, m := range []ModuleID{alpha, beta, gamma} {
		if err := tx.AddModule(m, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Bind(alpha, "self", forward(ref(alpha, "self"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(beta, "x", forward(ref(gamma, "y"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(gamma, "y", forward(ref(beta, "x"))); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)

	if _, err := loader.Resolve(ref(alpha, "self")); !errors.Is(err, ErrCycle) {
		t.Fatalf("self-loop: want ErrCycle, got %v", err)
	}
	if _, err := loader.Resolve(ref(beta, "x")); !errors.Is(err, ErrCycle) {
		t.Fatalf("multi-hop cycle: want ErrCycle, got %v", err)
	}
	// Repeated attempts keep rejecting and never produce a partial address.
	if _, err := loader.Resolve(ref(beta, "x")); !errors.Is(err, ErrCycle) {
		t.Fatalf("repeated cycle walk: want ErrCycle, got %v", err)
	}

	// A healthy chain must still resolve after cycles elsewhere were rejected.
	good := mod("good", "sha256:ok")
	tx = loader.Begin()
	if err := tx.AddModule(good, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(good, "run", concrete(0x5000)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)
	got, err := loader.Resolve(ref(good, "run"))
	if err != nil {
		t.Fatalf("healthy chain after cycle: %v", err)
	}
	if got.Addr != 0x5000 {
		t.Fatalf("want 0x5000, got %#x", got.Addr)
	}
}

// TestUpdateTransactionAndFixedGraph checks transaction isolation, atomic
// generation switches and the fixed-version-graph guarantee: each resolution
// pins one immutable generation for its whole walk.
func TestUpdateTransactionAndFixedGraph(t *testing.T) {
	loader := NewLoader()
	oldMod := mod("svc", "sha256:old")
	newMod := mod("svc", "sha256:new")

	tx := loader.Begin()
	if err := tx.AddModule(oldMod, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(oldMod, "call", concrete(0x1111)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)
	if loader.Generation() != 1 {
		t.Fatalf("generation = %d, want 1", loader.Generation())
	}

	// A discarded transaction leaves the installed graph untouched.
	tx = loader.Begin()
	if tx.BaseGeneration() != 1 {
		t.Fatalf("base generation = %d, want 1", tx.BaseGeneration())
	}
	if err := tx.AddModule(newMod, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(newMod, "call", concrete(0x2222)); err != nil {
		t.Fatal(err)
	}
	tx.Discard()
	if loader.Generation() != 1 {
		t.Fatalf("discard changed generation to %d", loader.Generation())
	}
	if _, err := loader.Resolve(ref(newMod, "call")); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("discarded tx leaked module: %v", err)
	}

	// A commit failing validation must not advance the generation.
	tx = loader.Begin()
	broken := mod("broken", "sha256:b")
	if err := tx.AddModule(broken, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(broken, "ok", concrete(0x3333)); err != nil {
		t.Fatal(err)
	}
	// Corrupt the private graph so only the commit-time whole-graph
	// validation can catch the dangling forwarding entry.
	ghost := mod("ghost", "sha256:gh")
	tx.next.bindings[ref(broken, "ok")] = &Binding{To: ref(ghost, "x")}
	wantErrIs(t, tx.Commit(), ErrUnknownModule)
	if loader.Generation() != 1 {
		t.Fatalf("failed commit advanced generation to %d", loader.Generation())
	}
	if _, err := loader.Resolve(ref(broken, "ok")); !errors.Is(err, ErrUnknownModule) {
		t.Fatalf("failed commit leaked partial graph: %v", err)
	}

	// Valid commit atomically installs the next fixed graph.
	tx = loader.Begin()
	if err := tx.AddModule(newMod, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(newMod, "call", concrete(0x2222)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)
	if loader.Generation() != 2 {
		t.Fatalf("generation = %d, want 2", loader.Generation())
	}

	oldRes, err := loader.Resolve(ref(oldMod, "call"))
	if err != nil {
		t.Fatalf("old identity: %v", err)
	}
	newRes, err := loader.Resolve(ref(newMod, "call"))
	if err != nil {
		t.Fatalf("new identity: %v", err)
	}
	if oldRes.Addr != 0x1111 || newRes.Addr != 0x2222 {
		t.Fatalf("generation switch mixed addresses: old=%#x new=%#x", oldRes.Addr, newRes.Addr)
	}

	// Using a closed transaction is rejected.
	wantErrIs(t, tx.AddModule(mod("late", "sha256:l"), true), ErrTxClosed)
	wantErrIs(t, tx.Commit(), ErrTxClosed)
}

// TestInvalidBindings covers malformed concrete and forwarding bindings.
func TestInvalidBindings(t *testing.T) {
	loader := NewLoader()
	m := mod("m", "sha256:1")

	tx := loader.Begin()
	if err := tx.AddModule(m, true); err != nil {
		t.Fatal(err)
	}
	wantErrIs(t, tx.Bind(m, "zero", concrete(0)), ErrInvalidAddress)
	wantErrIs(t, tx.Bind(m, "badfwd", Binding{}), ErrInvalidRef)
	wantErrIs(t, tx.AddModule(mod("", "no-name"), true), ErrInvalidRef)
	wantErrIs(t, tx.Bind(m, "", concrete(1)), ErrInvalidRef)
	tx.Discard()

	// Resolution entry points are validated too.
	if _, err := loader.Resolve(ref(m, "")); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("want ErrInvalidRef, got %v", err)
	}
}

// TestConcurrentResolve hammers the same chains from many goroutines to
// exercise the shared cache and graph locking.
func TestConcurrentResolve(t *testing.T) {
	loader := NewLoader()
	first := mod("first", "sha256:f")
	second := mod("second", "sha256:s")
	third := mod("third", "sha256:t")

	tx := loader.Begin()
	for _, m := range []ModuleID{first, second, third} {
		if err := tx.AddModule(m, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Bind(first, "go", forward(ref(second, "go"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(second, "go", forward(ref(third, "go"))); err != nil {
		t.Fatal(err)
	}
	if err := tx.Bind(third, "go", concrete(0x9000)); err != nil {
		t.Fatal(err)
	}
	mustCommit(t, tx)

	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				got, err := loader.Resolve(ref(first, "go"))
				if err != nil {
					errs <- err
					return
				}
				if got.Addr != 0x9000 {
					errs <- fmt.Errorf("got addr %#x", got.Addr)
					return
				}
			}
		}()
	}

	// Concurrently install new generations; in-flight resolutions must keep
	// using the generation they pinned.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 50; n++ {
			u := loader.Begin()
			if err := u.SetAuthorized(third, true); err != nil {
				errs <- err
				return
			}
			if err := u.Commit(); err != nil {
				errs <- err
				return
			}
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
