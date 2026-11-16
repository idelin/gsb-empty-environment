package netsec

import (
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	testerSA   uint16 = 0x0E00
	otherSA    uint16 = 0x0E01
	unknownSA  uint16 = 0x0E99
	allowedECU uint16 = 0x1001
	deniedECU  uint16 = 0x1FFF
)

func buildMsg(payloadType uint16, payload []byte) []byte {
	msg := make([]byte, HeaderLen+len(payload))
	msg[0] = 0x02
	msg[1] = 0xFD
	binary.BigEndian.PutUint16(msg[2:4], payloadType)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(payload)))
	copy(msg[HeaderLen:], payload)
	return msg
}

func activationMsg(sa uint16, actType byte) []byte {
	payload := []byte{byte(sa >> 8), byte(sa), actType, 0x00, 0x00, 0x00, 0x00}
	return buildMsg(PayloadTypeRoutingActivationRequest, payload)
}

func diagMsg(sa, ta uint16, data ...byte) []byte {
	payload := []byte{byte(sa >> 8), byte(sa), byte(ta >> 8), byte(ta)}
	payload = append(payload, data...)
	return buildMsg(PayloadTypeDiagnosticMessage, payload)
}

func newTestGate(now func() time.Time) *Gate {
	return NewGate(Config{
		MaxPayloadLen: 4096,
		IdleTimeout:   time.Minute,
		Registry: map[uint16][]uint16{
			testerSA: {allowedECU},
			otherSA:  {allowedECU, deniedECU},
		},
		Now: now,
	})
}

func TestVersionInverseMismatch(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	msg := activationMsg(testerSA, ActivationTypeDefault)
	msg[1] = 0x00 // inverse byte no longer complementary to version
	v := g.HandleMessage(s, msg)
	if !errors.Is(v.Err, ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", v.Err)
	}
	if !v.Close {
		t.Fatal("malformed header must close the connection")
	}
	if v.Accept {
		t.Fatal("malformed header must not be accepted")
	}
	if s.Activated() {
		t.Fatal("session must not become activated")
	}
}

func TestPayloadLengthExceedsCallerLimit(t *testing.T) {
	g := NewGate(Config{MaxPayloadLen: 4, Registry: map[uint16][]uint16{testerSA: {allowedECU}}})
	s := g.NewSession()

	v := g.HandleMessage(s, activationMsg(testerSA, ActivationTypeDefault))
	if !errors.Is(v.Err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", v.Err)
	}
	if !v.Close {
		t.Fatal("oversized payload must close the connection")
	}
}

func TestUnregisteredSourceAddress(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	v := g.HandleMessage(s, activationMsg(unknownSA, ActivationTypeDefault))
	if v.Accept {
		t.Fatal("unregistered source address must be rejected")
	}
	if v.Code != RcUnknownSourceAddress {
		t.Fatalf("expected response code 0x%02X, got 0x%02X", RcUnknownSourceAddress, v.Code)
	}
	if s.Activated() {
		t.Fatal("session must not become activated")
	}
}

func TestUnsupportedActivationType(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	v := g.HandleMessage(s, activationMsg(testerSA, 0x02))
	if v.Accept {
		t.Fatal("unsupported activation type must be rejected")
	}
	if v.Code != RcUnsupportedActivationType {
		t.Fatalf("expected response code 0x%02X, got 0x%02X", RcUnsupportedActivationType, v.Code)
	}

	for _, actType := range []byte{ActivationTypeDefault, ActivationTypeRegulatoryDiag} {
		g2 := newTestGate(nil)
		s2 := g2.NewSession()
		if v := g2.HandleMessage(s2, activationMsg(testerSA, actType)); !v.Accept {
			t.Fatalf("activation type 0x%02X must be accepted, got %q", actType, v.Reason)
		}
	}
}

func TestOccupancyConflictAndIdleTimeout(t *testing.T) {
	now := time.Now()
	mu := sync.Mutex{}
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}

	g := newTestGate(clock)
	first := g.NewSession()
	second := g.NewSession()

	if v := g.HandleMessage(first, activationMsg(testerSA, ActivationTypeDefault)); !v.Accept {
		t.Fatalf("first activation must succeed, got %q", v.Reason)
	}

	// Conflict: same source address from another connection is refused,
	// the existing owner is not replaced.
	v := g.HandleMessage(second, activationMsg(testerSA, ActivationTypeDefault))
	if v.Accept {
		t.Fatal("occupied source address must be refused, not replaced")
	}
	if v.Code != RcSocketsBusy {
		t.Fatalf("expected response code 0x%02X, got 0x%02X", RcSocketsBusy, v.Code)
	}
	if !first.Activated() || first.SourceAddr() != testerSA {
		t.Fatal("original owner must keep its activation")
	}

	// Activity on the owning connection refreshes the idle timer.
	advance(50 * time.Second)
	if v := g.HandleMessage(first, diagMsg(testerSA, allowedECU, 0x22, 0xF1, 0x90)); !v.Accept {
		t.Fatalf("diagnostic on owning connection must be admitted, got %q", v.Reason)
	}
	advance(50 * time.Second)
	if v := g.HandleMessage(second, activationMsg(testerSA, ActivationTypeDefault)); v.Accept {
		t.Fatal("recently active owner must not be evicted")
	}

	// After the idle timeout the new connection takes over and the stale
	// owner loses its activation.
	advance(61 * time.Second)
	if v := g.HandleMessage(second, activationMsg(testerSA, ActivationTypeDefault)); !v.Accept {
		t.Fatalf("idle-timed-out source address must be handed over, got %q", v.Reason)
	}
	if first.Activated() {
		t.Fatal("stale owner must lose its activation after idle timeout")
	}
	if v := g.HandleMessage(first, diagMsg(testerSA, allowedECU)); v.Accept || !v.Close {
		t.Fatal("evicted connection must be dropped and closed on further diagnostics")
	}
}

func TestDuplicateActivationSameConnection(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	if v := g.HandleMessage(s, activationMsg(testerSA, ActivationTypeDefault)); !v.Accept {
		t.Fatalf("first activation must succeed, got %q", v.Reason)
	}
	v := g.HandleMessage(s, activationMsg(testerSA, ActivationTypeDefault))
	if v.Accept {
		t.Fatal("duplicate activation on the same connection must be rejected")
	}
	if v.Code != RcAlreadyActive {
		t.Fatalf("expected response code 0x%02X, got 0x%02X", RcAlreadyActive, v.Code)
	}
}

func TestDiagnosticBeforeActivation(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	v := g.HandleMessage(s, diagMsg(testerSA, allowedECU, 0x22, 0xF1, 0x90))
	if v.Accept {
		t.Fatal("diagnostic before activation must be dropped")
	}
	if !v.Close {
		t.Fatal("diagnostic before activation must close the connection")
	}
}

func TestDiagnosticSourceAndTargetAuthorization(t *testing.T) {
	g := newTestGate(nil)
	s := g.NewSession()

	if v := g.HandleMessage(s, activationMsg(testerSA, ActivationTypeDefault)); !v.Accept {
		t.Fatalf("activation must succeed, got %q", v.Reason)
	}

	// Source address must match the one registered at activation.
	v := g.HandleMessage(s, diagMsg(otherSA, allowedECU))
	if v.Accept {
		t.Fatal("diagnostic with mismatched source address must be rejected")
	}
	if v.Code != DiagNackInvalidSA {
		t.Fatalf("expected NAK 0x%02X, got 0x%02X", DiagNackInvalidSA, v.Code)
	}

	// Target outside the authorized ECU set must be rejected.
	v = g.HandleMessage(s, diagMsg(testerSA, deniedECU))
	if v.Accept {
		t.Fatal("diagnostic to unauthorized target must be rejected")
	}
	if v.Code != DiagNackUnknownTA {
		t.Fatalf("expected NAK 0x%02X, got 0x%02X", DiagNackUnknownTA, v.Code)
	}

	// Authorized target is admitted.
	v = g.HandleMessage(s, diagMsg(testerSA, allowedECU, 0x22, 0xF1, 0x90))
	if !v.Accept {
		t.Fatalf("authorized diagnostic must be admitted, got %q", v.Reason)
	}
	if v.Code != DiagAck {
		t.Fatalf("expected ACK 0x%02X, got 0x%02X", DiagAck, v.Code)
	}
}
