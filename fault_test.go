package netsec

import (
	"errors"
	"testing"
)

// joinScenario returns the canonical multi-device join scenario as a list
// of steps. Steps are idempotent: after an injected crash and a full
// restart, re-running the interrupted step must safely resume.
func joinScenario(env *testEnv, net *maliciousNetwork, a, b [8]byte) []func() error {
	var reqA, reqB *JoinRequest
	var respA, respB *JoinAccept
	return []func() error{
		func() error { r, err := env.devices[a].BeginJoin(); reqA = r; return err },
		func() error { r, err := net.RoundTrip(reqA); respA = r; return err },
		func() error { return env.devices[a].HandleJoinAccept(respA) },
		func() error { r, err := env.devices[b].BeginJoin(); reqB = r; return err },
		func() error { r, err := net.RoundTrip(reqB); respB = r; return err },
		func() error { return env.devices[b].HandleJoinAccept(respB) },
		func() error { r, err := env.devices[a].BeginJoin(); reqA = r; return err },
		func() error { r, err := net.RoundTrip(reqA); respA = r; return err },
		func() error { return env.devices[a].HandleJoinAccept(respA) },
	}
}

func runSteps(t *testing.T, env *testEnv, steps []func() error) {
	t.Helper()
	for i, step := range steps {
		err := step()
		for errors.Is(err, errInjectedCrash) {
			// Power loss: restart every component from its persistent
			// stores, then resume the interrupted step.
			env.start()
			err = step()
		}
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}

// TestFaultAtEveryPersistencePoint injects a crash at each persistence
// point of the join protocol and verifies that no successful session ever
// reuses derivation inputs, JoinNonces stay strictly increasing, and the
// scenario still completes.
func TestFaultAtEveryPersistencePoint(t *testing.T) {
	const netID = 0x000013

	// Clean run to count the persistence points.
	clean := newTestEnv(t, netID)
	a := clean.addDevice(0x0A).DevEUI
	b := clean.addDevice(0x0B).DevEUI
	clean.start()
	runSteps(t, clean, joinScenario(clean, newMaliciousNetwork(clean), a, b))
	points := clean.totalCommits()
	if points == 0 {
		t.Fatal("no persistence points observed")
	}
	clean.checkInvariants()

	for point := 1; point <= points; point++ {
		t.Run("", func(t *testing.T) {
			env := newTestEnv(t, netID)
			env.addDevice(0x0A)
			env.addDevice(0x0B)
			inj := &crashInjector{countdown: point}
			env.attach(inj)
			env.start()
			runSteps(t, env, joinScenario(env, newMaliciousNetwork(env), a, b))
			env.detach()

			if !inj.hasFired() {
				t.Fatalf("fault at point %d never fired", point)
			}
			sessA, ok := env.devices[a].Session()
			if !ok {
				t.Fatalf("point %d: device A has no session", point)
			}
			if sessA.SessionGen != 2 {
				t.Fatalf("point %d: device A session gen = %d, want 2", point, sessA.SessionGen)
			}
			sessB, ok := env.devices[b].Session()
			if !ok {
				t.Fatalf("point %d: device B has no session", point)
			}
			if sessB.SessionGen != 1 {
				t.Fatalf("point %d: device B session gen = %d, want 1", point, sessB.SessionGen)
			}
			// One more restart for good measure: sessions must survive.
			env.start()
			if _, ok := env.devices[a].Session(); !ok {
				t.Fatalf("point %d: device A lost its session across restart", point)
			}
			env.checkInvariants()
		})
	}
}

// TestRollbackAtEveryDeviceSnapshot rolls the terminal store back to each
// earlier snapshot after the scenario completed, then rejoins, verifying
// that reused DevNonces never cause session key reuse.
func TestRollbackAtEveryDeviceSnapshot(t *testing.T) {
	const netID = 0x000013
	env := newTestEnv(t, netID)
	info := env.addDevice(0x0A)
	env.start()
	net := newMaliciousNetwork(env)
	st := env.devSt[info.DevEUI]

	var snaps [][]byte
	snaps = append(snaps, st.Snapshot()) // before any join
	wrap := st.BeforeCommit
	st.BeforeCommit = func() error {
		if wrap != nil {
			if err := wrap(); err != nil {
				return err
			}
		}
		snaps = append(snaps, st.Snapshot())
		return nil
	}

	dev := env.devices[info.DevEUI]
	joinOnce := func() {
		t.Helper()
		req, err := dev.BeginJoin()
		if err != nil {
			t.Fatalf("BeginJoin: %v", err)
		}
		resp, err := net.RoundTrip(req)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		if err := dev.HandleJoinAccept(resp); err != nil {
			t.Fatalf("HandleJoinAccept: %v", err)
		}
	}
	joinOnce()
	joinOnce()
	st.BeforeCommit = wrap

	// Roll back to every recorded snapshot and rejoin from there.
	for i, snap := range snaps {
		st.Restore(snap)
		env.start()
		dev = env.devices[info.DevEUI]
		req, err := dev.BeginJoin()
		if err != nil {
			t.Fatalf("snapshot %d: BeginJoin: %v", i, err)
		}
		resp, err := net.RoundTrip(req)
		if err != nil {
			// A reused DevNonce that the server never kept as a pending
			// reply is rejected; that is a safe outcome too.
			if !errors.Is(err, ErrDevNonceReused) {
				t.Fatalf("snapshot %d: Join: %v", i, err)
			}
			continue
		}
		if err := dev.HandleJoinAccept(resp); err != nil {
			t.Fatalf("snapshot %d: HandleJoinAccept: %v", i, err)
		}
	}
	env.checkInvariants()
}
