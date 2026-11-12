package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testHarness bundles a registry, a controllable clock and one signer per
// principal so every test can mint credentials deterministically.
type testHarness struct {
	registry *KeyRegistry
	signers  map[Identity]*SyntheticSigner
	clock    *fakeClock
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestHarness(t *testing.T, ids ...Identity) *testHarness {
	t.Helper()
	h := &testHarness{
		registry: NewKeyRegistry(),
		signers:  make(map[Identity]*SyntheticSigner),
		clock:    newFakeClock(),
	}
	for _, id := range ids {
		signer, publicKey, err := NewSyntheticSigner(id)
		if err != nil {
			t.Fatalf("generate key for %s: %v", id, err)
		}
		if err := h.registry.Register(signer, publicKey); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
		h.signers[id] = signer
	}
	return h
}

func (h *testHarness) engine() *Engine {
	return NewEngine(h.registry, WithClock(h.clock.Now))
}

func (h *testHarness) register(t *testing.T, identity Identity) {
	t.Helper()
	signer, publicKey, err := NewSyntheticSigner(identity)
	if err != nil {
		t.Fatalf("generate key for %s: %v", identity, err)
	}
	if err := h.registry.Register(signer, publicKey); err != nil {
		t.Fatalf("register %s: %v", identity, err)
	}
	h.signers[identity] = signer
}

func principal(tenant, service string) Identity {
	return Identity{TenantID: tenant, ServiceID: service}
}

// TestDiamondPath verifies two branches sharing a bootstrap token: both pin
// the same root digest, carry monotonically shrinking deadlines and verify
// independently.
func TestDiamondPath(t *testing.T) {
	host := principal("tenant-a", "host")
	gateway := principal("tenant-a", "gateway")
	billing := principal("tenant-a", "billing")
	audit := principal("tenant-a", "audit")
	h := newTestHarness(t, host, gateway, billing, audit)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], gateway, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	branch1, err := engine.Exchange(root, h.signers[gateway], billing, 30*time.Minute)
	if err != nil {
		t.Fatalf("branch1 hop: %v", err)
	}
	leaf1, err := engine.Exchange(branch1, h.signers[billing], audit, 30*time.Minute)
	if err != nil {
		t.Fatalf("branch1 leaf: %v", err)
	}

	// The same bootstrap credential fans out into a second branch.
	branch2, err := engine.Exchange(root, h.signers[gateway], audit, 45*time.Minute)
	if err != nil {
		t.Fatalf("branch2 hop: %v", err)
	}

	if err := engine.VerifyPath([]*Token{root, branch1, leaf1}); err != nil {
		t.Fatalf("branch1 path: %v", err)
	}
	if err := engine.VerifyPath([]*Token{root, branch2}); err != nil {
		t.Fatalf("branch2 path: %v", err)
	}

	rootDigest := root.Digest()
	if got := branch1.Ancestors; len(got) != 1 || got[0] != rootDigest {
		t.Fatalf("branch1 ancestors = %v, want only root digest", got)
	}
	if got := leaf1.Ancestors; len(got) != 2 || got[0] != rootDigest || got[1] != branch1.Digest() {
		t.Fatalf("leaf1 ancestors = %v, want full root->branch chain", got)
	}
	if got := branch2.Ancestors; len(got) != 1 || got[0] != rootDigest {
		t.Fatalf("branch2 ancestors = %v, want shared root digest", got)
	}
	if branch1.ChainID != root.ChainID || branch2.ChainID != root.ChainID || leaf1.ChainID != root.ChainID {
		t.Fatal("every hop must pin the bootstrap chain id")
	}
	if root.ChainID != root.rootDigest() {
		t.Fatal("bootstrap chain id must equal its own content digest")
	}

	if leaf1.RemainingHops != root.RemainingHops-2 {
		t.Fatalf("leaf1 remaining hops = %d, want %d", leaf1.RemainingHops, root.RemainingHops-2)
	}
	if !leaf1.EarliestExpiry.Equal(branch1.EarliestExpiry) {
		t.Fatalf("leaf expiry %v drifted from parent %v", leaf1.EarliestExpiry, branch1.EarliestExpiry)
	}
}

// TestCrossTenantSameName ensures identically named services in different
// tenants are distinct principals, while the same principal returning closes
// a cycle.
func TestCrossTenantSameName(t *testing.T) {
	host := principal("tenant-a", "host")
	alphaWorker := principal("tenant-a", "worker")
	betaWorker := principal("tenant-b", "worker")
	h := newTestHarness(t, host, alphaWorker, betaWorker)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], alphaWorker, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	cross, err := engine.Exchange(root, h.signers[alphaWorker], betaWorker, 30*time.Minute)
	if err != nil {
		t.Fatalf("cross-tenant hop with same service name: %v", err)
	}

	if _, err := engine.Exchange(cross, h.signers[betaWorker], alphaWorker, 30*time.Minute); !errors.Is(err, ErrCycle) {
		t.Fatalf("return to same-named principal err = %v, want ErrCycle", err)
	}
	if _, err := engine.Exchange(cross, h.signers[betaWorker], betaWorker, time.Minute); !errors.Is(err, ErrCycle) {
		t.Fatalf("self loop err = %v, want ErrCycle", err)
	}

	if err := engine.VerifyPath([]*Token{root, cross}); err != nil {
		t.Fatalf("valid cross-tenant path: %v", err)
	}
}

// TestCycleBackToHost proves a loop that returns to the bootstrap issuer is
// rejected even though that principal never appears as a token subject.
func TestCycleBackToHost(t *testing.T) {
	host := principal("tenant-a", "host")
	first := principal("tenant-a", "svc-1")
	second := principal("tenant-a", "svc-2")
	h := newTestHarness(t, host, first, second)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], first, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	middle, err := engine.Exchange(root, h.signers[first], second, 30*time.Minute)
	if err != nil {
		t.Fatalf("first hop: %v", err)
	}
	if _, err := engine.Exchange(middle, h.signers[second], host, 30*time.Minute); !errors.Is(err, ErrCycle) {
		t.Fatalf("return to host err = %v, want ErrCycle", err)
	}
}

// TestPresenterAndTtlGuards covers presenter mismatch, non-positive TTLs and
// unknown identities.
func TestPresenterAndTtlGuards(t *testing.T) {
	host := principal("tenant-a", "host")
	first := principal("tenant-a", "svc-1")
	other := principal("tenant-a", "intruder")
	h := newTestHarness(t, host, first, other)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], first, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := engine.Exchange(root, h.signers[other], principal("tenant-a", "x"), time.Minute); !errors.Is(err, ErrPresenterMismatch) {
		t.Fatalf("foreign presenter err = %v, want ErrPresenterMismatch", err)
	}
	if _, err := engine.Exchange(root, h.signers[first], other, 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("zero ttl err = %v, want ErrInvalidTTL", err)
	}
	rogue, _, err := NewSyntheticSigner(principal("tenant-a", "rogue"))
	if err != nil {
		t.Fatalf("rogue signer: %v", err)
	}
	if _, err := engine.IssueBootstrap(rogue, other, time.Hour); !errors.Is(err, ErrUnknownIdentity) {
		t.Fatalf("unregistered issuer err = %v, want ErrUnknownIdentity", err)
	}
}

// TestConcurrentReplay hammers one delegated token with parallel exchanges:
// exactly one must succeed, every other must be rejected as a replay.
func TestConcurrentReplay(t *testing.T) {
	host := principal("tenant-a", "host")
	front := principal("tenant-a", "front")
	back := principal("tenant-a", "back")
	downstream := principal("tenant-a", "downstream")
	h := newTestHarness(t, host, front, back, downstream)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], front, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	once, err := engine.Exchange(root, h.signers[front], back, 30*time.Minute)
	if err != nil {
		t.Fatalf("first delegation: %v", err)
	}

	const goroutines = 64
	var success int64
	var replays int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := engine.Exchange(once, h.signers[back], downstream, time.Minute)
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, ErrReplay):
				atomic.AddInt64(&replays, 1)
			default:
				t.Errorf("unexpected exchange error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("successful replayed exchanges = %d, want 1", success)
	}
	if replays != goroutines-1 {
		t.Fatalf("replay rejections = %d, want %d", replays, goroutines-1)
	}
}

// TestRejectCrossChainSplicing builds two independent chains and splices a
// leaf onto the other chain's prefix. Both signature binding (unsigned
// mutation) and explicit ancestry pinning (re-signed mutation) must reject it.
func TestRejectCrossChainSplicing(t *testing.T) {
	host := principal("tenant-a", "host")
	alice := principal("tenant-a", "alice")
	bob := principal("tenant-a", "bob")
	carol := principal("tenant-a", "carol")
	dave := principal("tenant-a", "dave")
	erin := principal("tenant-a", "erin")
	h := newTestHarness(t, host, alice, bob, carol, dave, erin)
	engine := h.engine()

	// Chain X: host -> alice -> bob -> carol.
	rootX, err := engine.IssueBootstrap(h.signers[host], alice, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap X: %v", err)
	}
	midX, err := engine.Exchange(rootX, h.signers[alice], bob, 30*time.Minute)
	if err != nil {
		t.Fatalf("chain X middle: %v", err)
	}
	leafX, err := engine.Exchange(midX, h.signers[bob], carol, 15*time.Minute)
	if err != nil {
		t.Fatalf("chain X leaf: %v", err)
	}

	// Chain Y: host -> dave -> bob, an independent chain whose middle hop is
	// also bob. Splicing leafX after it keeps every issuer/subject linkage
	// legal and every principal distinct; only the pinned ancestor digests
	// reveal that the leaf came from another chain.
	rootY, err := engine.IssueBootstrap(h.signers[host], dave, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap Y: %v", err)
	}
	midY, err := engine.Exchange(rootY, h.signers[dave], bob, 30*time.Minute)
	if err != nil {
		t.Fatalf("chain Y middle: %v", err)
	}

	// Unsigned mutation: the tampered copy fails signature verification.
	mutated := *leafX
	mutated.Ancestors = []Digest{rootY.Digest(), midY.Digest()}
	if err := engine.VerifyPath([]*Token{rootY, midY, &mutated}); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("unsigned splice err = %v, want ErrInvalidSignature", err)
	}

	// Re-signed mutation: per-token signatures become valid again, but
	// ancestry pinning still detects the cross-chain splice.
	resigned := mutated
	signed, err := h.signers[bob].Sign(resigned.signingBytes())
	if err != nil {
		t.Fatalf("resign: %v", err)
	}
	resigned.Signature = signed
	if err := engine.VerifyPath([]*Token{rootY, midY, &resigned}); !errors.Is(err, ErrAncestryMismatch) {
		t.Fatalf("re-signed splice err = %v, want ErrAncestryMismatch", err)
	}

	// A genuinely valid continuation of chain Y still verifies.
	validY, err := engine.Exchange(midY, h.signers[bob], erin, 15*time.Minute)
	if err != nil {
		t.Fatalf("chain Y continuation: %v", err)
	}

	// Genuine chains remain valid.
	if err := engine.VerifyPath([]*Token{rootX, midX, leafX}); err != nil {
		t.Fatalf("chain X broken: %v", err)
	}
	if err := engine.VerifyPath([]*Token{rootY, midY, validY}); err != nil {
		t.Fatalf("chain Y broken: %v", err)
	}
}

// TestDeadlineCannotBeRefreshed forces a short-lived ancestor and proves every
// descendant is capped by the earliest inherited deadline.
func TestDeadlineCannotBeRefreshed(t *testing.T) {
	host := principal("tenant-a", "host")
	svcA := principal("tenant-a", "svc-a")
	svcB := principal("tenant-a", "svc-b")
	svcC := principal("tenant-a", "svc-c")
	svcD := principal("tenant-a", "svc-d")
	h := newTestHarness(t, host, svcA, svcB, svcC, svcD)
	engine := h.engine()

	root, err := engine.IssueBootstrap(h.signers[host], svcA, 10*time.Minute)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	h.clock.Advance(2 * time.Minute)
	first, err := engine.Exchange(root, h.signers[svcA], svcB, 24*time.Hour)
	if err != nil {
		t.Fatalf("first hop: %v", err)
	}
	if !first.EarliestExpiry.Equal(root.EarliestExpiry) {
		t.Fatalf("first hop expiry %v exceeded root %v", first.EarliestExpiry, root.EarliestExpiry)
	}

	h.clock.Advance(3 * time.Minute)
	second, err := engine.Exchange(first, h.signers[svcB], svcC, 24*time.Hour)
	if err != nil {
		t.Fatalf("second hop: %v", err)
	}
	if second.EarliestExpiry.After(root.EarliestExpiry) {
		t.Fatalf("second hop refreshed ancestor deadline: %v > %v", second.EarliestExpiry, root.EarliestExpiry)
	}

	h.clock.Advance(6 * time.Minute)
	if _, err := engine.Exchange(second, h.signers[svcC], svcD, time.Minute); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("exchange past deadline err = %v, want ErrTokenExpired", err)
	}
}

// TestHopLimitExhausted verifies the hop budget decrements and blocks further
// delegation once it reaches zero.
func TestHopLimitExhausted(t *testing.T) {
	host := principal("tenant-a", "host")
	first := principal("tenant-a", "svc-1")
	second := principal("tenant-a", "svc-2")
	third := principal("tenant-a", "svc-3")
	h := newTestHarness(t, host, first, second, third)
	engine := NewEngine(h.registry, WithClock(h.clock.Now), WithMaxHops(1))

	root, err := engine.IssueBootstrap(h.signers[host], first, time.Hour)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if root.RemainingHops != 1 {
		t.Fatalf("root remaining hops = %d, want 1", root.RemainingHops)
	}

	leaf, err := engine.Exchange(root, h.signers[first], second, 30*time.Minute)
	if err != nil {
		t.Fatalf("only allowed hop: %v", err)
	}
	if leaf.RemainingHops != 0 {
		t.Fatalf("leaf remaining hops = %d, want 0", leaf.RemainingHops)
	}

	if _, err := engine.Exchange(leaf, h.signers[second], third, time.Minute); !errors.Is(err, ErrHopLimit) {
		t.Fatalf("exchange past hop limit err = %v, want ErrHopLimit", err)
	}
	if err := engine.VerifyPath([]*Token{root, leaf}); err != nil {
		t.Fatalf("valid path failed: %v", err)
	}
}
