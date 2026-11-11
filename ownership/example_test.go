//go:build darwin || linux

package ownership_test

import (
	"context"
	"time"

	"example.org/netsec/q0266/ownership"
)

// ExampleHost shows the host lifecycle: tag a tenant root, observe a reparented
// grandchild, record a group change, then run the seal-terminate-confirm
// cleanup protocol. It has no Output comment because it starts real processes
// and is compiled for documentation rather than executed by the test runner.
func ExampleHost() {
	tracker := ownership.NewTracker()
	host := ownership.NewHost(tracker)

	gen := ownership.Gen{TenantID: "tenant-7", Epoch: 1}

	// Start a tenant root in its own process group, tagged via EnvMarker.
	root, err := host.SpawnRoot(gen, "/bin/sleep", []string{"30"})
	if err != nil {
		panic(err)
	}

	// A reaper/adoption loop observing a grandchild (e.g. via a subreaper)
	// records it under the same generation. The marker it inherited is
	// available in its environment at observation time.
	if err := host.ObserveSpawn(ownership.Spawned{
		PID:        root.Process.Pid + 1,
		ParentPID:  root.Process.Pid,
		Generation: gen,
		PGID:       root.Process.Pid,
	}); err != nil {
		panic(err)
	}

	// setpgid/setsid changes are diagnostic only; ownership stays put.
	if err := host.ObserveGroupChanged(root.Process.Pid+1, 9999, 9999); err != nil {
		panic(err)
	}

	// Tenant exit: block new spawns, SIGTERM the reachable descendant set,
	// escalate to SIGKILL after 5s, and wait for every Exited confirmation.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := host.CleanupTenant(ctx, gen, 5*time.Second); err != nil {
		panic(err)
	}

	// Dead bookkeeping can now be reclaimed.
	tracker.PruneGeneration(gen)
}
