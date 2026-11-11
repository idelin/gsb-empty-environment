package ownership

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeKiller records signals but does not change process state: the test
// drives Exited events itself to model the kernel asynchronously reaping
// processes. This keeps the cleanup protocol fully deterministic.
type fakeKiller struct {
	mu        sync.Mutex
	termPIDs  []int
	forcePIDs []int
}

func (f *fakeKiller) Terminate(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.termPIDs = append(f.termPIDs, pid)
	return nil
}

func (f *fakeKiller) ForceKill(pid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forcePIDs = append(f.forcePIDs, pid)
	return nil
}

func (f *fakeKiller) terms() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.termPIDs...)
}

func (f *fakeKiller) forces() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.forcePIDs...)
}

func mustApply(t *testing.T, tr *Tracker, ev Event) {
	t.Helper()
	if err := tr.Apply(ev); err != nil {
		t.Fatalf("Apply(%#v): %v", ev, err)
	}
}

func assertExactPIDs(t *testing.T, got []int, want ...int) {
	t.Helper()
	seen := make(map[int]bool, len(got))
	for _, pid := range got {
		if seen[pid] {
			t.Fatalf("duplicate pid %d in %v", pid, got)
		}
		seen[pid] = true
	}
	for _, pid := range want {
		if !seen[pid] {
			t.Fatalf("missing pid %d in %v, want %v", pid, got, want)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
}

func waitForSignalCount(t *testing.T, f *fakeKiller, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.terms()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d SIGTERM deliveries, got %v", want, f.terms())
}

func waitForForceCount(t *testing.T, f *fakeKiller, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.forces()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d SIGKILL deliveries, got %v", want, f.forces())
}

// TestInheritanceAndImmutability establishes the core invariant: generation is
// assigned at creation and neither parent exits nor group/session changes can
// move a process between tenants.
func TestInheritanceAndImmutability(t *testing.T) {
	tr := NewTracker()
	g1 := Gen{TenantID: "acme", Epoch: 1}
	g2 := Gen{TenantID: "globex", Epoch: 1}

	mustApply(t, tr, Spawned{PID: 100, Generation: g1, PGID: 100})
	mustApply(t, tr, Spawned{PID: 101, ParentPID: 100, Generation: g1, PGID: 100})
	mustApply(t, tr, Spawned{PID: 200, Generation: g2, PGID: 200})

	// A child cannot claim a generation different from its tracked parent.
	err := tr.Apply(Spawned{PID: 102, ParentPID: 100, Generation: g2})
	if !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("cross-generation child: got %v, want ErrGenerationMismatch", err)
	}

	// Child becomes a session leader via setsid. Metadata moves, ownership
	// does not.
	mustApply(t, tr, GroupChanged{PID: 101, PGID: 101, SID: 101, FromPGID: 100})
	if gen, ok := tr.GenerationOf(101); !ok || gen != g1 {
		t.Fatalf("generation after setsid: got %v ok=%v, want %v", gen, ok, g1)
	}

	// Parent exits first; the orphaned child must still be a g1 member.
	mustApply(t, tr, Exited{PID: 100})
	if gen, ok := tr.GenerationOf(101); !ok || gen != g1 {
		t.Fatalf("orphan generation: got %v ok=%v, want %v", gen, ok, g1)
	}
	assertExactPIDs(t, tr.LiveMembers(g1), 101)
	assertExactPIDs(t, tr.LiveMembers(g2), 200)

	// Reachable closure from the dead root still finds the orphan.
	assertExactPIDs(t, tr.ReachableFrom(100), 101)
}

// TestParentExitsFirst drives cleanup through Shutdown to prove the orphaned
// descendant set is terminated even though the root process is already gone.
func TestParentExitsFirst(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	g := Gen{TenantID: "acme", Epoch: 1}

	mustApply(t, tr, Spawned{PID: 300, Generation: g, PGID: 300})
	mustApply(t, tr, Spawned{PID: 301, ParentPID: 300, Generation: g, PGID: 300})
	mustApply(t, tr, Spawned{PID: 302, ParentPID: 301, Generation: g, PGID: 300})
	mustApply(t, tr, Exited{PID: 300})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Shutdown(ctx, tr, g, ShutdownConfig{
			GracePeriod: 50 * time.Millisecond,
			Killer:      killer,
		})
	}()

	waitForSignalCount(t, killer, 2)
	if !tr.IsSealed(g) {
		t.Fatal("generation not sealed before signals")
	}
	assertExactPIDs(t, killer.terms(), 301, 302)

	// New spawns into the sealed generation are rejected while cleanup runs.
	err := tr.Apply(Spawned{PID: 303, ParentPID: 301, Generation: g})
	if !errors.Is(err, ErrGenerationSealed) {
		t.Fatalf("spawn during cleanup: got %v, want ErrGenerationSealed", err)
	}

	// The kernel eventually reaps the two surviving descendants.
	mustApply(t, tr, Exited{PID: 301})
	mustApply(t, tr, Exited{PID: 302})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not confirm termination")
	}
	if len(killer.forces()) != 0 {
		t.Fatalf("SIGKILL escalated for processes that exited on SIGTERM: %v", killer.forces())
	}
}

// TestRegroupDoesNotChangeOwnership models a child calling setsid and another
// child joining a foreign process group: both must still be killed via their
// immutable generation, never via group-based targeting.
func TestRegroupDoesNotChangeOwnership(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	g := Gen{TenantID: "acme", Epoch: 1}

	mustApply(t, tr, Spawned{PID: 400, Generation: g, PGID: 400})
	mustApply(t, tr, Spawned{PID: 401, ParentPID: 400, Generation: g, PGID: 400})
	mustApply(t, tr, Spawned{PID: 402, ParentPID: 400, Generation: g, PGID: 400})

	// 401 becomes a session leader; 402 joins an unrelated group 9999.
	mustApply(t, tr, GroupChanged{PID: 401, PGID: 401, SID: 401, FromPGID: 400})
	mustApply(t, tr, GroupChanged{PID: 402, PGID: 9999, FromPGID: 400})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Shutdown(ctx, tr, g, ShutdownConfig{
			GracePeriod: 50 * time.Millisecond,
			Killer:      killer,
		})
	}()

	waitForSignalCount(t, killer, 3)
	assertExactPIDs(t, killer.terms(), 400, 401, 402)

	for _, pid := range []int{400, 401, 402} {
		mustApply(t, tr, Exited{PID: pid})
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not finish after group changes")
	}
}

// TestPIDReuseDoesNotKillNewTenant is the critical safety case. An old
// tenant's PID is recycled by the kernel for processes of other tenants;
// cleanup of the old tenant must never signal a reincarnation.
func TestPIDReuseDoesNotKillNewTenant(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	old := Gen{TenantID: "acme", Epoch: 1}
	fresh := Gen{TenantID: "acme", Epoch: 2}
	other := Gen{TenantID: "globex", Epoch: 7}

	mustApply(t, tr, Spawned{PID: 500, Generation: old, PGID: 500})
	mustApply(t, tr, Spawned{PID: 501, ParentPID: 500, Generation: old, PGID: 500})

	// Old tenant root dies and its PID gets recycled for a different tenant.
	mustApply(t, tr, Exited{PID: 500})
	mustApply(t, tr, Spawned{PID: 500, Generation: other, PGID: 7000})

	// Same PID later serves the old tenant's next generation. Identity follows
	// the new Gen, not the numeric PID.
	mustApply(t, tr, Exited{PID: 500})
	mustApply(t, tr, Spawned{PID: 500, Generation: fresh, PGID: 500})

	if gen, ok := tr.GenerationOf(500); !ok || gen != fresh {
		t.Fatalf("reused pid generation: got %v ok=%v, want %v", gen, ok, fresh)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Shutdown(ctx, tr, old, ShutdownConfig{
			GracePeriod: 50 * time.Millisecond,
			Killer:      killer,
		})
	}()

	// Only the genuinely old descendant 501 may be signalled; the reused PID
	// 500 belongs to another generation and must be untouched.
	waitForSignalCount(t, killer, 1)
	assertExactPIDs(t, killer.terms(), 501)
	mustApply(t, tr, Exited{PID: 501})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown old generation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not confirm old generation cleanup")
	}
	if gen, ok := tr.GenerationOf(500); !ok || gen != fresh {
		t.Fatalf("new-tenant process killed or reclassified: gen=%v ok=%v", gen, ok)
	}
	if !tr.CanSpawn(fresh) {
		t.Fatal("sealing an old generation blocked the current generation")
	}
}

// TestForceKillAfterGrace verifies SIGTERM -> wait -> SIGKILL escalation.
func TestForceKillAfterGrace(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	g := Gen{TenantID: "stuck", Epoch: 1}
	mustApply(t, tr, Spawned{PID: 600, Generation: g, PGID: 600})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Shutdown(ctx, tr, g, ShutdownConfig{
			GracePeriod: 20 * time.Millisecond,
			Killer:      killer,
		})
	}()

	waitForSignalCount(t, killer, 1)
	assertExactPIDs(t, killer.terms(), 600)
	waitForForceCount(t, killer, 1)
	assertExactPIDs(t, killer.forces(), 600)

	mustApply(t, tr, Exited{PID: 600})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not confirm after SIGKILL")
	}
}

// TestShutdownTimeout surfaces survivors instead of silently succeeding.
func TestShutdownTimeout(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	g := Gen{TenantID: "stuck", Epoch: 2}
	mustApply(t, tr, Spawned{PID: 700, Generation: g})
	mustApply(t, tr, Spawned{PID: 701, ParentPID: 700, Generation: g})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	err := Shutdown(ctx, tr, g, ShutdownConfig{
		GracePeriod: 10 * time.Millisecond,
		Killer:      killer,
	})

	var se *ShutdownError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want *ShutdownError", err)
	}
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("err does not wrap ErrShutdownTimeout: %v", err)
	}
	assertExactPIDs(t, se.Survivors, 700, 701)
	if !tr.IsSealed(g) {
		t.Fatal("generation must stay sealed after timeout")
	}
}

// TestShutdownEmptyGeneration seals and confirms an empty generation without
// touching the Killer.
func TestShutdownEmptyGeneration(t *testing.T) {
	tr := NewTracker()
	killer := &fakeKiller{}
	g := Gen{TenantID: "vacant", Epoch: 1}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Shutdown(ctx, tr, g, ShutdownConfig{Killer: killer}); err != nil {
		t.Fatalf("Shutdown empty generation: %v", err)
	}
	if !tr.IsSealed(g) {
		t.Fatal("empty generation still sealed")
	}
	if len(killer.terms()) != 0 || len(killer.forces()) != 0 {
		t.Fatal("killer invoked for empty generation")
	}
}

// TestEventValidation covers the registry's event-level guard rails.
func TestEventValidation(t *testing.T) {
	tr := NewTracker()
	g := Gen{TenantID: "acme", Epoch: 1}

	if err := tr.Apply(Spawned{PID: 1, Generation: Gen{}}); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("invalid gen spawn: %v", err)
	}
	if err := tr.Apply(Exited{PID: 99}); !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("unknown exit: %v", err)
	}
	if err := tr.Apply(GroupChanged{PID: 99, PGID: 1}); !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("unknown group change: %v", err)
	}

	mustApply(t, tr, Spawned{PID: 800, Generation: g, PGID: 800})
	if err := tr.Apply(Spawned{PID: 800, Generation: g}); !errors.Is(err, ErrPIDInUse) {
		t.Fatalf("live pid reuse: %v", err)
	}
	mustApply(t, tr, Sealed{Generation: g})
	if err := tr.Apply(Spawned{PID: 801, Generation: g}); !errors.Is(err, ErrGenerationSealed) {
		t.Fatalf("sealed spawn: %v", err)
	}
	// Re-sealing is idempotent.
	mustApply(t, tr, Sealed{Generation: g})

	if err := Shutdown(context.Background(), tr, g, ShutdownConfig{}); err == nil {
		t.Fatal("Shutdown without Killer must fail")
	}
}

// TestGenRoundTrip checks the environment-marker encoding.
func TestGenRoundTrip(t *testing.T) {
	g := Gen{TenantID: "tenant-a", Epoch: 42}
	parsed, err := ParseGen(g.String())
	if err != nil {
		t.Fatalf("ParseGen: %v", err)
	}
	if parsed != g {
		t.Fatalf("round trip: got %v, want %v", parsed, g)
	}
	for _, bad := range []string{"", ":1", "tenant:", "tenant:notanum", "tenant:1:2"} {
		if _, err := ParseGen(bad); err == nil {
			t.Fatalf("ParseGen(%q) succeeded", bad)
		}
	}
}

// TestPruneGeneration reclaims dead bookkeeping while preserving live edges.
func TestPruneGeneration(t *testing.T) {
	tr := NewTracker()
	g := Gen{TenantID: "acme", Epoch: 1}
	mustApply(t, tr, Spawned{PID: 900, Generation: g})
	mustApply(t, tr, Spawned{PID: 901, ParentPID: 900, Generation: g})
	mustApply(t, tr, Exited{PID: 900})

	if removed := tr.PruneGeneration(g); removed != 0 {
		t.Fatalf("pruned %d nodes while live descendant exists", removed)
	}
	mustApply(t, tr, Exited{PID: 901})
	if removed := tr.PruneGeneration(g); removed != 2 {
		t.Fatalf("pruned %d nodes, want 2", removed)
	}
	if _, ok := tr.Lookup(900); ok {
		t.Fatal("dead node still present after prune")
	}
	if len(tr.Members(g)) != 0 {
		t.Fatalf("membership retained after prune: %v", tr.Members(g))
	}
}
