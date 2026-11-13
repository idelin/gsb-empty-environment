package netsec

import (
	"errors"
	"sync"
	"testing"
)

// errInjectedCrash simulates a power-loss / crash at a persistence point.
var errInjectedCrash = errors.New("netsec: injected crash")

// testEnv bundles a server, devices and the recording key store.
type testEnv struct {
	t       *testing.T
	ks      *KeyStore
	reg     *Registry
	netID   uint32
	srvSt   *MemStore
	devSt   map[[8]byte]*MemStore
	trusted map[[8]byte]*MemTrustedCounter
	infos   map[[8]byte]*DeviceInfo
	server  *Server
	devices map[[8]byte]*Device
	limit   uint32 // 0 means default 24-bit ceiling

	mu          sync.Mutex
	derivations []DerivationInputs
	allocated   map[[8]byte][]uint32
}

func newTestEnv(t *testing.T, netID uint32) *testEnv {
	t.Helper()
	env := &testEnv{
		t:       t,
		ks:      NewKeyStore(),
		reg:     NewRegistry(),
		netID:   netID,
		srvSt:   &MemStore{},
		devSt:   make(map[[8]byte]*MemStore),
		trusted: make(map[[8]byte]*MemTrustedCounter),
		infos:   make(map[[8]byte]*DeviceInfo),
		devices: make(map[[8]byte]*Device),
	}
	env.ks.OnDerive = func(in DerivationInputs) {
		env.mu.Lock()
		env.derivations = append(env.derivations, in)
		env.mu.Unlock()
	}
	return env
}

// addDevice registers a fresh device with fixed DevEUI/JoinEUI.
func (env *testEnv) addDevice(devEUIByte byte) *DeviceInfo {
	env.t.Helper()
	var devEUI, joinEUI [8]byte
	devEUI[7] = devEUIByte
	joinEUI[7] = 0xE0
	info, err := NewDeviceInfo(env.ks, devEUI, joinEUI)
	if err != nil {
		env.t.Fatalf("NewDeviceInfo: %v", err)
	}
	if err := env.reg.Register(info); err != nil {
		env.t.Fatalf("Register: %v", err)
	}
	env.infos[devEUI] = info
	env.devSt[devEUI] = &MemStore{}
	env.trusted[devEUI] = &MemTrustedCounter{}
	return info
}

// start (re)builds the server and all devices from their stores,
// modelling a full restart of every component.
func (env *testEnv) start() {
	env.t.Helper()
	var opts []ServerOption
	if env.limit != 0 {
		opts = append(opts, WithJoinNonceLimit(env.limit))
	}
	srv, err := NewServer(env.reg, env.ks, env.srvSt, env.netID, opts...)
	if err != nil {
		env.t.Fatalf("NewServer: %v", err)
	}
	srv.OnJoinAllocated = func(devEUI [8]byte, joinNonce uint32, _ uint64) {
		env.mu.Lock()
		env.allocated[devEUI] = append(env.allocated[devEUI], joinNonce)
		env.mu.Unlock()
	}
	env.allocated = make(map[[8]byte][]uint32)
	env.server = srv
	for devEUI, info := range env.infos {
		dev, err := NewDevice(info, env.ks, env.devSt[devEUI], env.trusted[devEUI])
		if err != nil {
			env.t.Fatalf("NewDevice: %v", err)
		}
		env.devices[devEUI] = dev
	}
}

// checkInvariants verifies the global safety properties collected so far.
func (env *testEnv) checkInvariants() {
	env.t.Helper()
	env.mu.Lock()
	defer env.mu.Unlock()

	seen := make(map[string]int)
	for i, d := range env.derivations {
		key := string(d.Bytes())
		if j, dup := seen[key]; dup {
			env.t.Fatalf("derivation inputs repeated: sessions %d and %d share %x", j, i, key)
		}
		seen[key] = i
	}
	for devEUI, nonces := range env.allocated {
		for i := 1; i < len(nonces); i++ {
			if nonces[i] <= nonces[i-1] {
				env.t.Fatalf("device %x: JoinNonce not strictly increasing: %v", devEUI, nonces)
			}
		}
		for _, n := range nonces {
			if n == 0 || n > JoinNonceMax24 {
				env.t.Fatalf("device %x: JoinNonce %d out of 24-bit range", devEUI, n)
			}
		}
	}
}

// maliciousNetwork sits between devices and the server. It records every
// exchange so tests can replay, duplicate or cross-forward messages.
type maliciousNetwork struct {
	env *testEnv

	mu        sync.Mutex
	exchanges []recordedExchange
}

type recordedExchange struct {
	req  *JoinRequest
	resp *JoinAccept
	err  error
}

func newMaliciousNetwork(env *testEnv) *maliciousNetwork {
	return &maliciousNetwork{env: env}
}

// RoundTrip delivers a JoinRequest to the server and records the exchange.
func (n *maliciousNetwork) RoundTrip(req *JoinRequest) (*JoinAccept, error) {
	resp, err := n.env.server.Join(req)
	n.mu.Lock()
	n.exchanges = append(n.exchanges, recordedExchange{req: req, resp: resp, err: err})
	n.mu.Unlock()
	return resp, err
}

// replayResponse returns a previously recorded JoinAccept for re-delivery.
func (n *maliciousNetwork) replayResponse(i int) *JoinAccept {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.exchanges[i].resp
}

func (n *maliciousNetwork) numExchanges() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.exchanges)
}

// crashInjector fails exactly one commit (the countdown-th) across all
// attached stores, then disarms itself.
type crashInjector struct {
	mu        sync.Mutex
	countdown int
	fired     bool
}

func (c *crashInjector) hook() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.countdown <= 0 {
		return nil
	}
	c.countdown--
	if c.countdown == 0 {
		c.fired = true
		return errInjectedCrash
	}
	return nil
}

func (c *crashInjector) hasFired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

// attach installs the injector hook on every store in the environment.
func (env *testEnv) attach(c *crashInjector) {
	env.srvSt.BeforeCommit = c.hook
	for _, st := range env.devSt {
		st.BeforeCommit = c.hook
	}
	for _, tc := range env.trusted {
		tc.BeforeCommit = c.hook
	}
}

// detach removes all fault hooks.
func (env *testEnv) detach() {
	env.srvSt.BeforeCommit = nil
	for _, st := range env.devSt {
		st.BeforeCommit = nil
	}
	for _, tc := range env.trusted {
		tc.BeforeCommit = nil
	}
}

// totalCommits counts commits across every store in the environment.
func (env *testEnv) totalCommits() int {
	total := env.srvSt.Commits()
	for _, st := range env.devSt {
		total += st.Commits()
	}
	// Trusted counters do not expose a commit count; approximate via epoch.
	for _, tc := range env.trusted {
		v, _ := tc.LoadEpoch()
		total += int(v)
	}
	return total
}
