package discovery_test

import (
	"fmt"
	"testing"
	"time"

	"seedmesh/discovery"
)

func testConfig(netw *discovery.MemNetwork, id, addr string, seeds []string) discovery.Config {
	return discovery.Config{
		ID:                     id,
		Addr:                   addr,
		Seeds:                  seeds,
		GossipInterval:         50 * time.Millisecond,
		BootstrapRetryInterval: 100 * time.Millisecond,
		SampleSize:             10,
		DialTimeout:            500 * time.Millisecond,
		Listen:                 netw.Listen,
		Dial:                   netw.Dial,
	}
}

func startNode(t *testing.T, netw *discovery.MemNetwork, id, addr string, seeds []string) *discovery.Node {
	t.Helper()
	n := discovery.NewNode(testConfig(netw, id, addr, seeds))
	if err := n.Start(); err != nil {
		t.Fatalf("start %s: %v", id, err)
	}
	return n
}

func stopAll(nodes ...*discovery.Node) {
	for _, n := range nodes {
		n.Stop()
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout after %s waiting for: %s", timeout, what)
}

// Acceptance test: 3 brand-new nodes configured with a single seed
// address must pull the metadata of the whole 50-node network via
// bootstrap within 1 second and immediately join the gossip ring.
func TestColdStartBootstrapAndGossipJoin(t *testing.T) {
	const clusterSize = 50
	netw := discovery.NewMemNetwork()

	// Phase 0: form a 50-node cluster out of a single seed.
	seed := startNode(t, netw, "seed-0", "127.0.0.1:0", nil)
	cluster := []*discovery.Node{seed}
	for i := 1; i < clusterSize; i++ {
		id := fmt.Sprintf("node-%02d", i)
		cluster = append(cluster, startNode(t, netw, id, "127.0.0.1:0", []string{seed.Addr()}))
	}
	defer stopAll(cluster...)
	waitFor(t, 5*time.Second, "seed sees all 50 members", func() bool {
		return seed.MemberCount() == clusterSize
	})

	// Phase 1: cold-start 3 fresh nodes knowing only the seed address.
	var fresh []*discovery.Node
	start := time.Now()
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("fresh-%d", i)
		fresh = append(fresh, startNode(t, netw, id, "127.0.0.1:0", []string{seed.Addr()}))
	}
	defer stopAll(fresh...)
	elapsed := time.Since(start)

	// Each fresh node must hold the full 50-node snapshot right after
	// its synchronous bootstrap handshake, well within 1 second.
	for _, f := range fresh {
		if got := f.MemberCount(); got < clusterSize {
			t.Fatalf("%s pulled only %d members via bootstrap, want >= %d", f.ID(), got, clusterSize)
		}
	}
	if elapsed > time.Second {
		t.Fatalf("bootstrap of 3 nodes took %s, exceeds the 1s bound", elapsed)
	}
	t.Logf("3 fresh nodes pulled >=%d members via bootstrap in %s", clusterSize, elapsed)

	// fresh-0 bootstrapped before fresh-1 and fresh-2 existed, so it
	// can only learn about them through the gossip shuffle ring.
	if fresh[0].Knows(fresh[1].ID()) || fresh[0].Knows(fresh[2].ID()) {
		t.Fatalf("fresh-0 learned about later peers during bootstrap, test premise broken")
	}

	// Phase 2: the fresh nodes merge into the gossip ring; the whole
	// 53-node mesh converges without any further bootstrap.
	total := clusterSize + 3
	waitFor(t, 5*time.Second, "all 53 nodes converge via gossip", func() bool {
		for _, n := range cluster {
			if n.MemberCount() != total {
				return false
			}
		}
		for _, f := range fresh {
			if f.MemberCount() != total {
				return false
			}
		}
		return true
	})
	for _, f := range fresh {
		if f.GossipRounds() == 0 {
			t.Fatalf("%s never gossiped", f.ID())
		}
		if !f.Knows("node-49") || !fresh[0].Knows(fresh[2].ID()) {
			t.Fatalf("%s view incomplete after gossip convergence", f.ID())
		}
	}
	t.Logf("gossip ring converged: all %d nodes know all %d members", total, total)
}

// An orphan node whose seed is not up yet must keep retrying the
// handshake and join as soon as the seed appears.
func TestOrphanNodeJoinsWhenSeedAppears(t *testing.T) {
	netw := discovery.NewMemNetwork()
	// The seed will bind this address later; nothing listens yet.
	seedAddr := "seed-late.mem"

	orphan := startNode(t, netw, "orphan", "127.0.0.1:0", []string{seedAddr})
	defer orphan.Stop()
	if orphan.Bootstrapped() {
		t.Fatal("orphan should not be bootstrapped while its seed is down")
	}
	if orphan.MemberCount() != 1 {
		t.Fatalf("orphan should know only itself, knows %d", orphan.MemberCount())
	}

	// The seed comes up later; the orphan's retry loop must find it.
	seed := startNode(t, netw, "seed-late", seedAddr, nil)
	defer seed.Stop()

	waitFor(t, 3*time.Second, "orphan retries bootstrap and joins", func() bool {
		return orphan.Bootstrapped() && orphan.Knows("seed-late")
	})
	waitFor(t, 3*time.Second, "seed learns about orphan", func() bool {
		return seed.Knows("orphan")
	})
}

// A node with several seeds must join through whichever seed is
// reachable, even if the first entries of the list are dead.
func TestBootstrapFailsOverToLiveSeed(t *testing.T) {
	netw := discovery.NewMemNetwork()
	dead := "dead-seed.mem" // never bound
	seed := startNode(t, netw, "seed-live", "127.0.0.1:0", nil)
	defer seed.Stop()

	n := startNode(t, netw, "joiner", "127.0.0.1:0", []string{dead, seed.Addr()})
	defer n.Stop()

	if !n.Bootstrapped() {
		t.Fatal("joiner should bootstrap via the live seed")
	}
	if !n.Knows("seed-live") {
		t.Fatal("joiner should know the live seed after bootstrap")
	}
	waitFor(t, 3*time.Second, "seed registers joiner", func() bool {
		return seed.Knows("joiner")
	})
}
