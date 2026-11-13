package netsec_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0712"
)

const (
	testVersion       byte   = 0x02
	testEntityAddress uint16 = 0x0e00
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestGateway(clock *fakeClock) *netsec.Gateway {
	return netsec.NewGateway(netsec.GatewayConfig{
		EntityAddress:    testEntityAddress,
		MaxPayloadLength: 64,
		IdleTimeout:      60 * time.Second,
		Now:              clock.Now,
		SourceAuthorizations: map[uint16][]uint16{
			0x0e00: {0x0e01, 0x0e02},
			0x0e10: {0x0e20},
			0x0e30: {0x0e01},
		},
	})
}

func doipFrame(payloadType uint16, payload []byte) []byte {
	frame := make([]byte, netsec.HeaderSize+len(payload))
	frame[0] = testVersion
	frame[1] = ^testVersion
	binary.BigEndian.PutUint16(frame[2:4], payloadType)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[netsec.HeaderSize:], payload)
	return frame
}

func activationPayload(source uint16, activationType byte) []byte {
	payload := make([]byte, 7)
	binary.BigEndian.PutUint16(payload[0:2], source)
	payload[2] = activationType
	return payload
}

func diagnosticPayload(source, target uint16, data ...byte) []byte {
	payload := make([]byte, 4+len(data))
	binary.BigEndian.PutUint16(payload[0:2], source)
	binary.BigEndian.PutUint16(payload[2:4], target)
	copy(payload[4:], data)
	return payload
}

func responseCode(t *testing.T, frame []byte) byte {
	t.Helper()
	if len(frame) == 0 {
		t.Fatal("expected activation response, got no frame")
	}
	header, payload, err := netsec.ParseFrame(frame, 64)
	if err != nil {
		t.Fatalf("parse activation response: %v", err)
	}
	if header.PayloadType != netsec.PayloadTypeRoutingActivationResponse {
		t.Fatalf("response payload type = 0x%04x, want 0x0006", header.PayloadType)
	}
	if len(payload) != 9 {
		t.Fatalf("response payload length = %d, want 9", len(payload))
	}
	return payload[4]
}

func activate(t *testing.T, session *netsec.Session, source uint16, activationType byte, now time.Time) netsec.Decision {
	t.Helper()
	return session.HandleAt(
		doipFrame(netsec.PayloadTypeRoutingActivationRequest, activationPayload(source, activationType)),
		now,
	)
}

func TestVersionInverseMustBeComplement(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	session := gateway.NewSession("bad-inverse")

	frame := doipFrame(netsec.PayloadTypeRoutingActivationRequest, activationPayload(0x0e00, 0x00))
	frame[1] ^= 0x01

	decision := session.HandleAt(frame, clock.Now())
	if decision.Accepted || decision.Deliver {
		t.Fatalf("bad inverse was accepted: %+v", decision)
	}
	if !decision.Close {
		t.Fatal("bad inverse must close the connection")
	}
	if decision.Reason != netsec.ReasonVersionInverseMismatch {
		t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonVersionInverseMismatch)
	}
	if session.Activated() {
		t.Fatal("session became activated on a malformed header")
	}
}

func TestPayloadLengthAboveCallerLimitIsRejected(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := netsec.NewGateway(netsec.GatewayConfig{
		MaxPayloadLength: 8,
		IdleTimeout:      60 * time.Second,
		Now:              clock.Now,
	})
	session := gateway.NewSession("oversized")

	payload := make([]byte, 9)
	binary.BigEndian.PutUint16(payload[0:2], 0x0e00)
	decision := session.HandleAt(
		doipFrame(netsec.PayloadTypeRoutingActivationRequest, payload),
		clock.Now(),
	)
	if decision.Accepted || !decision.Close {
		t.Fatalf("oversized payload decision = %+v, want rejected and closed", decision)
	}
	if decision.Reason != netsec.ReasonPayloadTooLarge {
		t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonPayloadTooLarge)
	}

	// ReadFrame must reject from the header alone, before allocating or
	// consuming a body.
	headerOnly := doipFrame(netsec.PayloadTypeDiagnosticMessage, make([]byte, 9))[:netsec.HeaderSize]
	if _, _, err := netsec.ReadFrame(bytes.NewReader(headerOnly), 8); !errors.Is(err, netsec.ErrPayloadTooLarge) {
		t.Fatalf("ReadFrame error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestRoutingActivationAdmission(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)

	t.Run("valid types", func(t *testing.T) {
		defaultSession := gateway.NewSession("default-type")
		decision := activate(t, defaultSession, 0x0e00, netsec.ActivationTypeDefault, clock.Now())
		if !decision.Accepted || decision.Close {
			t.Fatalf("default activation decision = %+v", decision)
		}
		if got := responseCode(t, decision.Response); got != netsec.RoutingActivationSuccess {
			t.Fatalf("response code = 0x%02x, want success", got)
		}

		wwhobdSession := gateway.NewSession("wwhobd-type")
		decision = activate(t, wwhobdSession, 0x0e10, netsec.ActivationTypeWWHOBD, clock.Now())
		if !decision.Accepted {
			t.Fatalf("WWH-OBD activation decision = %+v", decision)
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		session := gateway.NewSession("bad-type")
		decision := activate(t, session, 0x0e01, 0x02, clock.Now())
		if decision.Accepted {
			t.Fatal("invalid activation type was accepted")
		}
		if decision.Reason != netsec.ReasonInvalidActivationType {
			t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonInvalidActivationType)
		}
		if got := responseCode(t, decision.Response); got != netsec.RoutingActivationInvalidActivationType {
			t.Fatalf("response code = 0x%02x, want 0x07", got)
		}
		if session.Activated() {
			t.Fatal("invalid activation type activated the session")
		}
	})

	t.Run("unregistered source", func(t *testing.T) {
		session := gateway.NewSession("unknown-source")
		decision := activate(t, session, 0x0e99, netsec.ActivationTypeDefault, clock.Now())
		if decision.Accepted {
			t.Fatal("unregistered source was accepted")
		}
		if decision.Reason != netsec.ReasonUnknownSourceAddress {
			t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonUnknownSourceAddress)
		}
		if got := responseCode(t, decision.Response); got != netsec.RoutingActivationUnknownSourceAddress {
			t.Fatalf("response code = 0x%02x, want 0x01", got)
		}
	})

	t.Run("repeated activation", func(t *testing.T) {
		session := gateway.NewSession("repeat")
		first := activate(t, session, 0x0e30, netsec.ActivationTypeDefault, clock.Now())
		if !first.Accepted {
			t.Fatalf("first activation failed: %+v", first)
		}
		second := activate(t, session, 0x0e30, netsec.ActivationTypeDefault, clock.Now())
		if second.Accepted {
			t.Fatal("repeated activation was accepted")
		}
		if second.Reason != netsec.ReasonAlreadyActivated {
			t.Fatalf("reason = %v, want %v", second.Reason, netsec.ReasonAlreadyActivated)
		}
		if source, ok := session.ActivatedSource(); !ok || source != 0x0e30 {
			t.Fatalf("activation was changed by repeat: source=0x%02x ok=%v", source, ok)
		}
	})
}

func TestSourceLeaseConflictDoesNotReplaceOwner(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	owner := gateway.NewSession("owner")
	attacker := gateway.NewSession("attacker")

	if decision := activate(t, owner, 0x0e00, 0x00, clock.Now()); !decision.Accepted {
		t.Fatalf("owner activation failed: %+v", decision)
	}

	decision := activate(t, attacker, 0x0e00, 0x00, clock.Now())
	if decision.Accepted {
		t.Fatal("conflicting connection replaced the owner")
	}
	if decision.Reason != netsec.ReasonSourceAddressInUse {
		t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonSourceAddressInUse)
	}
	if got := responseCode(t, decision.Response); got != netsec.RoutingActivationSourceAlreadyActive {
		t.Fatalf("response code = 0x%02x, want 0x03", got)
	}
	if attacker.Activated() {
		t.Fatal("attacker session became activated")
	}
	if !owner.Activated() {
		t.Fatal("owner lost its activation after conflict")
	}

	ownedDiagnostic := owner.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e01)),
		clock.Now(),
	)
	if !ownedDiagnostic.Deliver {
		t.Fatalf("owner diagnostic stopped working: %+v", ownedDiagnostic)
	}

	owner.Close()
	decision = activate(t, attacker, 0x0e00, 0x00, clock.Now())
	if !decision.Accepted {
		t.Fatalf("source was not released by Close: %+v", decision)
	}
}

func TestIdleOwnerIsReclaimedAndRevoked(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	oldOwner := gateway.NewSession("old-owner")
	newOwner := gateway.NewSession("new-owner")

	if decision := activate(t, oldOwner, 0x0e00, 0x00, clock.Now()); !decision.Accepted {
		t.Fatalf("old owner activation failed: %+v", decision)
	}

	clock.Advance(59 * time.Second)
	refresh := oldOwner.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e01)),
		clock.Now(),
	)
	if !refresh.Deliver {
		t.Fatalf("valid diagnostic should refresh idle timer: %+v", refresh)
	}

	clock.Advance(59 * time.Second)
	conflict := activate(t, newOwner, 0x0e00, 0x00, clock.Now())
	if conflict.Accepted {
		t.Fatal("active owner was reclaimed before idle timeout")
	}

	clock.Advance(2 * time.Second)
	reclaimed := activate(t, newOwner, 0x0e00, 0x00, clock.Now())
	if !reclaimed.Accepted {
		t.Fatalf("idle owner was not reclaimed: %+v", reclaimed)
	}
	if oldOwner.Activated() {
		t.Fatal("reclaimed session still reports activation")
	}

	oldDiagnostic := oldOwner.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e01)),
		clock.Now(),
	)
	if oldDiagnostic.Deliver || !oldDiagnostic.Close {
		t.Fatalf("revoked owner diagnostic = %+v, want drop and close", oldDiagnostic)
	}

	newDiagnostic := newOwner.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e02)),
		clock.Now(),
	)
	if !newDiagnostic.Deliver {
		t.Fatalf("new owner diagnostic failed: %+v", newDiagnostic)
	}
}

func TestDiagnosticBeforeActivationIsDroppedAndCloses(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	session := gateway.NewSession("unactivated")

	decision := session.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e01, 0x22, 0xf1, 0x90)),
		clock.Now(),
	)
	if decision.Accepted || decision.Deliver {
		t.Fatalf("unactivated diagnostic was delivered: %+v", decision)
	}
	if !decision.Close {
		t.Fatal("unactivated diagnostic must close the connection")
	}
	if decision.Reason != netsec.ReasonNotActivated {
		t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonNotActivated)
	}
}

func TestDiagnosticSourceMustMatchActivation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	session := gateway.NewSession("source-spoof")

	if decision := activate(t, session, 0x0e00, 0x00, clock.Now()); !decision.Accepted {
		t.Fatalf("activation failed: %+v", decision)
	}

	decision := session.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e10, 0x0e01)),
		clock.Now(),
	)
	if decision.Accepted || decision.Deliver || !decision.Close {
		t.Fatalf("spoofed diagnostic = %+v, want drop, close, and no delivery", decision)
	}
	if decision.Reason != netsec.ReasonDiagnosticSourceMismatch {
		t.Fatalf("reason = %v, want %v", decision.Reason, netsec.ReasonDiagnosticSourceMismatch)
	}
}

func TestDiagnosticTargetMustBeAuthorized(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	session := gateway.NewSession("target-check")

	if decision := activate(t, session, 0x0e00, 0x00, clock.Now()); !decision.Accepted {
		t.Fatalf("activation failed: %+v", decision)
	}

	unauthorized := session.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e20, 0x22, 0xf1, 0x90)),
		clock.Now(),
	)
	if unauthorized.Accepted || unauthorized.Deliver || !unauthorized.Close {
		t.Fatalf("unauthorized target = %+v, want drop, close, and no delivery", unauthorized)
	}
	if unauthorized.Reason != netsec.ReasonTargetAddressNotAuthorized {
		t.Fatalf("reason = %v, want %v", unauthorized.Reason, netsec.ReasonTargetAddressNotAuthorized)
	}

	authorized := session.HandleAt(
		doipFrame(netsec.PayloadTypeDiagnosticMessage, diagnosticPayload(0x0e00, 0x0e01, 0x22, 0xf1, 0x90)),
		clock.Now(),
	)
	if !authorized.Accepted || !authorized.Deliver || authorized.Close {
		t.Fatalf("authorized diagnostic = %+v, want delivery", authorized)
	}
	if authorized.Diagnostic == nil {
		t.Fatal("authorized diagnostic has no parsed message")
	}
	if authorized.Diagnostic.SourceAddress != 0x0e00 || authorized.Diagnostic.TargetAddress != 0x0e01 {
		t.Fatalf("diagnostic addresses = 0x%02x -> 0x%02x", authorized.Diagnostic.SourceAddress, authorized.Diagnostic.TargetAddress)
	}
	if !bytes.Equal(authorized.Diagnostic.Data, []byte{0x22, 0xf1, 0x90}) {
		t.Fatalf("diagnostic data = %x", authorized.Diagnostic.Data)
	}
}

func TestReadFrameAndHandlePartsIntegration(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)
	session := gateway.NewSession("stream-loop")
	frame := doipFrame(netsec.PayloadTypeRoutingActivationRequest, activationPayload(0x0e00, 0x00))

	header, payload, err := netsec.ReadFrame(bytes.NewReader(frame), gateway.MaxPayloadLength())
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	decision := session.HandlePartsAt(header, payload, clock.Now())
	if !decision.Accepted {
		t.Fatalf("HandleParts rejected stream frame: %+v", decision)
	}
}

func TestConcurrentConflictAllowsExactlyOneOwner(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	gateway := newTestGateway(clock)

	const connections = 12
	var wg sync.WaitGroup
	wg.Add(connections)
	results := make(chan netsec.Decision, connections)
	for i := 0; i < connections; i++ {
		go func(i int) {
			defer wg.Done()
			session := gateway.NewSession(string(rune('a' + i)))
			results <- activate(t, session, 0x0e00, 0x00, clock.Now())
		}(i)
	}
	wg.Wait()
	close(results)

	accepted := 0
	for decision := range results {
		if decision.Accepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted activations = %d, want exactly 1", accepted)
	}
}
