package netsec

import (
	"crypto/rand"
	"sync"
	"testing"
)

const (
	testTenantA = "tenant-a"
	testTenantB = "tenant-b"
	baseTime    = uint64(1_794_000_000)
)

type fakeClock struct {
	mu  sync.Mutex
	now uint64
}

func newFakeClock(start uint64) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) get() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(seconds uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += seconds
}

type policyStore struct {
	mu       sync.Mutex
	policies map[string]Policy
}

func newPolicyStore(policies ...Policy) *policyStore {
	store := &policyStore{policies: make(map[string]Policy)}
	for _, policy := range policies {
		store.policies[policy.Tenant] = policy
	}
	return store
}

func (s *policyStore) PolicyForTenant(tenant string) (Policy, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.policies[tenant]
	return policy, ok
}

func (s *policyStore) replace(policy Policy) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[policy.Tenant] = policy
}

type countingFactory struct {
	mu      sync.Mutex
	calls   int
	lastReq SessionRequest
}

func (f *countingFactory) create(req SessionRequest) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastReq = req
	return &Session{
		Tenant:      req.Tenant,
		AuthLevel:   req.AuthLevel,
		Options:     req.Options,
		SessionData: append([]byte(nil), req.SessionData...),
	}, nil
}

func (f *countingFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func tls13Options(alpn ...string) ProtocolOptions {
	return ProtocolOptions{MinVersion: 0x0304, MaxVersion: 0x0304, ALPN: alpn}
}

type testHarness struct {
	clock   *fakeClock
	store   *policyStore
	keyring *Keyring
	factory *countingFactory
	manager *Manager
}

func newTestHarness(t *testing.T, gracePeriod uint64, policies ...Policy) *testHarness {
	t.Helper()
	clock := newFakeClock(baseTime)
	store := newPolicyStore(policies...)
	keyring, err := NewKeyring(testKey(t), clock.get(), gracePeriod)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	factory := &countingFactory{}
	manager, err := NewManager(keyring, store, factory.create, WithClock(clock.get))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	return &testHarness{
		clock:   clock,
		store:   store,
		keyring: keyring,
		factory: factory,
		manager: manager,
	}
}

func policyFor(tenant string, level AuthLevel, ttl uint64, alpn ...string) Policy {
	return Policy{
		Tenant:       tenant,
		MinAuthLevel: level,
		Options:      tls13Options(alpn...),
		TicketTTL:    ttl,
	}
}
