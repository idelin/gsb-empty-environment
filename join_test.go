package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestJoinHappyPath(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)

	dev := env.devices[info.DevEUI]
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
	sess, ok := dev.Session()
	if !ok {
		t.Fatal("no session after successful join")
	}
	if sess.JoinEpoch != 1 || sess.SessionGen != 1 {
		t.Fatalf("unexpected session epoch/gen: %+v", sess)
	}
	if got := env.server.JoinNonceUsed(info.DevEUI); got != 1 {
		t.Fatalf("server JoinNonceUsed = %d, want 1", got)
	}
	env.checkInvariants()
}

func TestDuplicateJoinRequestReturnsOriginal(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)
	dev := env.devices[info.DevEUI]

	req, err := dev.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin: %v", err)
	}
	resp1, err := net.RoundTrip(req)
	if err != nil {
		t.Fatalf("first Join: %v", err)
	}
	// Response lost: the device retransmits the identical request.
	resp2, err := net.RoundTrip(req)
	if err != nil {
		t.Fatalf("duplicate Join: %v", err)
	}
	if !bytes.Equal(resp1.Ciphertext, resp2.Ciphertext) || resp1.MIC != resp2.MIC {
		t.Fatal("duplicate request did not return the original JoinAccept")
	}
	if got := env.server.JoinNonceUsed(info.DevEUI); got != 1 {
		t.Fatalf("duplicate request consumed a new JoinNonce: %d", got)
	}
	// Same DevNonce with a tampered MIC must be rejected.
	bad := *req
	bad.MIC[0] ^= 0xFF
	if _, err := net.RoundTrip(&bad); !errors.Is(err, ErrMICMismatch) {
		t.Fatalf("tampered MIC: got %v, want ErrMICMismatch", err)
	}
	env.checkInvariants()
}

func TestUnknownDeviceAndJoinEUI(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)
	dev := env.devices[info.DevEUI]

	req, err := dev.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin: %v", err)
	}
	unknown := *req
	unknown.DevEUI[7] = 0x7F
	if _, err := net.RoundTrip(&unknown); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("unknown DevEUI: got %v, want ErrUnknownDevice", err)
	}
	wrongEUI := *req
	wrongEUI.JoinEUI[7] = 0x99
	if _, err := net.RoundTrip(&wrongEUI); !errors.Is(err, ErrJoinEUIMismatch) {
		t.Fatalf("wrong JoinEUI: got %v, want ErrJoinEUIMismatch", err)
	}
	env.checkInvariants()
}

// TestDevNonceRollback simulates a terminal whose ordinary storage rolls
// back across a power loss, reusing a DevNonce. The server must answer
// the byte-identical retransmission with the original JoinAccept, and the
// re-derived session key must differ from the pre-rollback one because
// the trusted joinEpoch moved forward.
func TestDevNonceRollback(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)
	dev := env.devices[info.DevEUI]

	req1, err := dev.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin: %v", err)
	}
	resp1, err := net.RoundTrip(req1)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if err := dev.HandleJoinAccept(resp1); err != nil {
		t.Fatalf("HandleJoinAccept: %v", err)
	}
	sess1, _ := dev.Session()
	key1, err := env.ks.KeyBytes(sess1.KeyHandle)
	if err != nil {
		t.Fatalf("KeyBytes: %v", err)
	}

	// Power loss: ordinary storage rolls back to before the join, the
	// trusted epoch does not. DevNonce is reused.
	st := env.devSt[info.DevEUI]
	st.Restore(nil)
	env.start()
	dev = env.devices[info.DevEUI]

	req2, err := dev.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin after rollback: %v", err)
	}
	if req2.DevNonce != req1.DevNonce {
		t.Fatalf("expected DevNonce reuse after rollback, got %d vs %d", req2.DevNonce, req1.DevNonce)
	}
	resp2, err := net.RoundTrip(req2)
	if err != nil {
		t.Fatalf("retransmission after rollback rejected: %v", err)
	}
	if !bytes.Equal(resp1.Ciphertext, resp2.Ciphertext) {
		t.Fatal("identical retransmission did not return the original JoinAccept")
	}
	if err := dev.HandleJoinAccept(resp2); err != nil {
		t.Fatalf("HandleJoinAccept after rollback: %v", err)
	}
	sess2, _ := dev.Session()
	key2, err := env.ks.KeyBytes(sess2.KeyHandle)
	if err != nil {
		t.Fatalf("KeyBytes: %v", err)
	}
	if bytes.Equal(key1, key2) {
		t.Fatal("session key reused across DevNonce rollback")
	}
	if sess2.JoinEpoch <= sess1.JoinEpoch {
		t.Fatalf("joinEpoch not monotonic: %d then %d", sess1.JoinEpoch, sess2.JoinEpoch)
	}
	env.checkInvariants()
}

// TestOldJoinAcceptCannotOverwriteNewSession verifies replayed JoinAccepts
// are rejected while a newer session exists, and that even in the worst
// case (full storage rollback) an accepted replay derives a fresh key.
func TestOldJoinAcceptCannotOverwriteNewSession(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)
	dev := env.devices[info.DevEUI]
	st := env.devSt[info.DevEUI]

	joinOnce := func() *JoinAccept {
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
		return resp
	}

	resp1 := joinOnce()
	snapAfterFirst := st.Snapshot()
	resp2 := joinOnce()
	sess2, _ := dev.Session()

	// Attacker replays the older JoinAccept while no join is pending.
	if err := dev.HandleJoinAccept(resp1); !errors.Is(err, ErrNoPendingJoin) {
		t.Fatalf("replay without pending: got %v, want ErrNoPendingJoin", err)
	}
	// Attacker replays it while a new join is pending: DevNonce mismatch.
	if _, err := dev.BeginJoin(); err != nil {
		t.Fatalf("BeginJoin: %v", err)
	}
	if err := dev.HandleJoinAccept(resp1); !errors.Is(err, ErrDevNonceMismatch) {
		t.Fatalf("replay with pending: got %v, want ErrDevNonceMismatch", err)
	}
	// And the newer JoinAccept itself cannot be replayed once accepted.
	if err := dev.HandleJoinAccept(resp2); !errors.Is(err, ErrDevNonceMismatch) {
		t.Fatalf("re-delivery of consumed accept: got %v, want ErrDevNonceMismatch", err)
	}
	cur, _ := dev.Session()
	if cur.KeyHandle != sess2.KeyHandle {
		t.Fatal("old JoinAccept overwrote the current session")
	}

	// Worst case: storage rolls back to just after the first join, so the
	// pending request matches the replayed JoinAccept's DevNonce again.
	st.Restore(snapAfterFirst)
	env.start()
	dev = env.devices[info.DevEUI]
	req, err := dev.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin after rollback: %v", err)
	}
	// The pending request is for the *second* DevNonce; resp1 binds the
	// first, so it must be rejected.
	if err := dev.HandleJoinAccept(resp1); !errors.Is(err, ErrDevNonceMismatch) {
		t.Fatalf("stale accept after rollback: got %v, want ErrDevNonceMismatch", err)
	}
	// The matching retransmission still completes via the server's stored
	// reply, deriving a fresh key thanks to the monotonic epoch.
	resp, err := net.RoundTrip(req)
	if err != nil {
		t.Fatalf("Join after rollback: %v", err)
	}
	if err := dev.HandleJoinAccept(resp); err != nil {
		t.Fatalf("HandleJoinAccept after rollback: %v", err)
	}
	sess3, _ := dev.Session()
	if sess3.KeyHandle == sess2.KeyHandle {
		t.Fatal("session key handle reused after rollback")
	}
	env.checkInvariants()
}

// TestCrossDeviceForwarding delivers one device's JoinAccept to another
// device and forwards requests with foreign identities.
func TestCrossDeviceForwarding(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	infoA := env.addDevice(0x0A)
	infoB := env.addDevice(0x0B)
	env.start()
	net := newMaliciousNetwork(env)
	devA := env.devices[infoA.DevEUI]
	devB := env.devices[infoB.DevEUI]

	reqA, err := devA.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin A: %v", err)
	}
	respA, err := net.RoundTrip(reqA)
	if err != nil {
		t.Fatalf("Join A: %v", err)
	}

	// B has its own pending request; A's JoinAccept is forwarded to B.
	if _, err := devB.BeginJoin(); err != nil {
		t.Fatalf("BeginJoin B: %v", err)
	}
	if err := devB.HandleJoinAccept(respA); !errors.Is(err, ErrAcceptAuthFailed) {
		t.Fatalf("cross-device accept: got %v, want ErrAcceptAuthFailed", err)
	}

	// A bit-flipped ciphertext must also fail authentication.
	forged := &JoinAccept{Ciphertext: append([]byte(nil), respA.Ciphertext...), MIC: respA.MIC}
	forged.Ciphertext[len(forged.Ciphertext)-1] ^= 0x01
	if err := devA.HandleJoinAccept(forged); !errors.Is(err, ErrAcceptAuthFailed) {
		t.Fatalf("forged ciphertext: got %v, want ErrAcceptAuthFailed", err)
	}

	// The legitimate exchange still completes for both devices.
	if err := devA.HandleJoinAccept(respA); err != nil {
		t.Fatalf("HandleJoinAccept A: %v", err)
	}
	reqB, err := devB.BeginJoin()
	if err != nil {
		t.Fatalf("BeginJoin B: %v", err)
	}
	respB, err := net.RoundTrip(reqB)
	if err != nil {
		t.Fatalf("Join B: %v", err)
	}
	if err := devB.HandleJoinAccept(respB); err != nil {
		t.Fatalf("HandleJoinAccept B: %v", err)
	}
	env.checkInvariants()
}

// TestJoinNonceExhaustion drives the server to the JoinNonce ceiling and
// verifies permanent rejection without wraparound, across restarts.
func TestJoinNonceExhaustion(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	env.limit = 2
	info := env.addDevice(0x01)
	env.start()
	net := newMaliciousNetwork(env)
	dev := env.devices[info.DevEUI]

	joinOnce := func() error {
		req, err := dev.BeginJoin()
		if err != nil {
			t.Fatalf("BeginJoin: %v", err)
		}
		resp, err := net.RoundTrip(req)
		if err != nil {
			return err
		}
		return dev.HandleJoinAccept(resp)
	}

	if err := joinOnce(); err != nil {
		t.Fatalf("join 1: %v", err)
	}
	if err := joinOnce(); err != nil {
		t.Fatalf("join 2: %v", err)
	}
	// The ceiling is reached: permanent rejection, no wraparound.
	if err := joinOnce(); !errors.Is(err, ErrJoinNonceExhausted) {
		t.Fatalf("join 3: got %v, want ErrJoinNonceExhausted", err)
	}
	if got := env.server.JoinNonceUsed(info.DevEUI); got != 2 {
		t.Fatalf("JoinNonce wrapped or regressed: %d", got)
	}
	// Restart the server: the rejection must persist.
	env.start()
	dev = env.devices[info.DevEUI]
	if err := joinOnce(); !errors.Is(err, ErrJoinNonceExhausted) {
		t.Fatalf("join after restart: got %v, want ErrJoinNonceExhausted", err)
	}
	if got := env.server.JoinNonceUsed(info.DevEUI); got != 2 {
		t.Fatalf("JoinNonce moved after exhaustion: %d", got)
	}
	env.checkInvariants()
}

// TestServerRestart verifies persistent server state across restarts:
// seen DevNonces stay consumed, JoinNonces keep increasing, and
// retransmissions are still answered from the stored reply.
func TestServerRestart(t *testing.T) {
	env := newTestEnv(t, 0x000013)
	infoA := env.addDevice(0x0A)
	infoB := env.addDevice(0x0B)
	env.start()
	net := newMaliciousNetwork(env)

	join := func(devEUI [8]byte) *JoinRequest {
		t.Helper()
		dev := env.devices[devEUI]
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
		return req
	}

	reqA1 := join(infoA.DevEUI)
	env.start() // server restart #1
	reqA2 := join(infoA.DevEUI)
	if reqA2.DevNonce == reqA1.DevNonce {
		t.Fatal("device reused DevNonce without rollback")
	}
	env.start() // server restart #2
	join(infoB.DevEUI)

	// Retransmitting A's latest request after restarts returns the
	// original JoinAccept (the server kept its stored reply), proving the
	// seen-set and reply survived.
	resp, err := net.RoundTrip(reqA2)
	if err != nil {
		t.Fatalf("retransmission after restarts: %v", err)
	}
	if !bytes.Equal(resp.Ciphertext, net.replayResponse(1).Ciphertext) {
		t.Fatal("retransmission after restart did not return original JoinAccept")
	}
	// A superseded request stays consumed even across restarts.
	if _, err := net.RoundTrip(reqA1); !errors.Is(err, ErrDevNonceReused) {
		t.Fatalf("superseded request: got %v, want ErrDevNonceReused", err)
	}
	if got := env.server.JoinNonceUsed(infoA.DevEUI); got != 2 {
		t.Fatalf("JoinNonceUsed A = %d, want 2", got)
	}
	if got := env.server.JoinNonceUsed(infoB.DevEUI); got != 1 {
		t.Fatalf("JoinNonceUsed B = %d, want 1", got)
	}
	env.checkInvariants()
}
