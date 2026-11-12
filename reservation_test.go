package netsec

import (
	"errors"
	"testing"
)

const (
	portA PortID = "initiator-a"
	portB PortID = "initiator-b"
	portC PortID = "initiator-c"
	admin PortID = "mgmt-console"

	keyA Key = 0x1111
	keyB Key = 0x2222
	keyC Key = 0x3333
)

func mustDispatch(t *testing.T, m *Machine, req Request) *Response {
	t.Helper()
	resp, err := m.Dispatch(req)
	if err != nil {
		t.Fatalf("Dispatch(%v): unexpected error: %v", req.Op, err)
	}
	return resp
}

func wantError(t *testing.T, err error, code Code, sentinel error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var prErr *PRError
	if !errors.As(err, &prErr) {
		t.Fatalf("error %v is not a *PRError", err)
	}
	if prErr.Code != code {
		t.Fatalf("code = %v, want %v", prErr.Code, code)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("errors.Is(err, %v) = false, got %v", sentinel, err)
	}
}

func register(t *testing.T, m *Machine, port PortID, key Key, aptpl bool) {
	t.Helper()
	mustDispatch(t, m, Request{
		Op: OpRegister, Source: port, NewKey: key, APTPL: aptpl,
	})
}

// A key stolen from portA must not be reusable from a different port: the
// binding is between key and the transport port identifier, not the key alone.
func TestStolenKeyCannotBeUsedFromAnotherPort(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)

	// Attacker on portB presents the stolen key for its own registration.
	_, err := m.Dispatch(Request{Op: OpRegister, Source: portB, Key: keyA, NewKey: keyB})
	wantError(t, err, CodeKeyMismatch, ErrKeyMismatch)

	// Registering the stolen key value as a brand-new key is also refused
	// because keys must be unique across registrants.
	_, err = m.Dispatch(Request{Op: OpRegister, Source: portB, NewKey: keyA})
	wantError(t, err, CodeKeyInUse, ErrKeyInUse)

	// The stolen key cannot drive privileged actions from the attacker port.
	_, err = m.Dispatch(Request{Op: OpPreempt, Source: portB, Key: keyA, PreemptKey: keyA})
	wantError(t, err, CodeNotRegistered, ErrNotRegistered)

	if m.Registered(portB) {
		t.Fatal("attacker port must remain unregistered")
	}

	// The legitimate owner still works from its own port.
	if !m.Registered(portA) {
		t.Fatal("victim registration must be untouched")
	}
}

func TestUnregisteredAndWrongKeyPreemptRejected(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)
	register(t, m, portB, keyB, false)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})

	// Unregistered initiator cannot preempt at all.
	_, err := m.Dispatch(Request{Op: OpPreempt, Source: portC, Key: keyC, PreemptKey: keyA})
	wantError(t, err, CodeNotRegistered, ErrNotRegistered)

	// Registered port presenting a forged/incorrect key is rejected.
	_, err = m.Dispatch(Request{Op: OpPreempt, Source: portB, Key: keyC, PreemptKey: keyA})
	wantError(t, err, CodeKeyMismatch, ErrKeyMismatch)

	// Nothing changed: victim stays registered and still holds the reservation.
	if !m.Registered(portA) {
		t.Fatal("victim registration removed by failed preempt")
	}
	resp := mustDispatch(t, m, Request{Op: OpReadReservation, Source: portB})
	if resp.Reservation == nil || resp.Reservation.Holder != portA {
		t.Fatalf("reservation holder = %v, want %v", resp.Reservation, portA)
	}
}

func TestPreemptRemovesVictimRegistrationAndTransfersReservation(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)
	register(t, m, portB, keyB, false)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})

	genBefore := m.Generation()
	resp := mustDispatch(t, m, Request{
		Op: OpPreemptAndAbort, Source: portB, Key: keyB, PreemptKey: keyA,
	})

	if !resp.Aborted {
		t.Fatal("PREEMPT AND ABORT response must carry Aborted")
	}
	if m.Registered(portA) {
		t.Fatal("preempted registrant must be removed")
	}
	if resp.Reservation == nil || resp.Reservation.Holder != portB {
		t.Fatalf("reservation holder = %v, want %v", resp.Reservation, portB)
	}
	if resp.Generation != genBefore+1 {
		t.Fatalf("generation = %d, want %d", resp.Generation, genBefore+1)
	}

	// The preempted port loses WERO write access; surviving registrant keeps it.
	if m.WriteAllowed(portA) {
		t.Fatal("preempted port retained write access under WERO")
	}
	if !m.WriteAllowed(portB) {
		t.Fatal("preemptor lost write access")
	}

	// A plain PREEMPT must not set the abort flag.
	register(t, m, portA, keyA, false)
	resp = mustDispatch(t, m, Request{Op: OpPreempt, Source: portB, Key: keyB, PreemptKey: keyA})
	if resp.Aborted {
		t.Fatal("plain PREEMPT must not set Aborted")
	}
}

func TestClearRestrictedToPolicyPorts(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})

	// A registered but non-management port cannot CLEAR.
	_, err := m.Dispatch(Request{Op: OpClear, Source: portA})
	wantError(t, err, CodeClearNotAuthorized, ErrClearNotAuthorized)

	// Rejected CLEAR leaves state and generation untouched.
	gen := m.Generation()
	if !m.Registered(portA) || gen == 0 {
		t.Fatal("unauthorized CLEAR modified state")
	}

	// The management-designated port can clear everything.
	resp := mustDispatch(t, m, Request{Op: OpClear, Source: admin})
	if m.Registered(portA) {
		t.Fatal("registration survived authorized CLEAR")
	}
	if resp.Reservation != nil {
		t.Fatal("reservation survived CLEAR")
	}
	if resp.Generation != gen+1 {
		t.Fatalf("generation = %d, want %d", resp.Generation, gen+1)
	}
}

func TestPowerLossAppliesAPTPL(t *testing.T) {
	// No APTPL anywhere: power loss wipes all state and resets generation.
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)
	register(t, m, portB, keyB, false)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})
	m.PowerLoss()

	if m.Registered(portA) || m.Registered(portB) {
		t.Fatal("non-APTPL registrations survived power loss")
	}
	if m.Generation() != 0 {
		t.Fatalf("generation after full wipe = %d, want 0", m.Generation())
	}
	if m.WriteAllowed(portA) != true {
		t.Fatal("unreserved LU should be writable by any port after wipe")
	}

	// APTPL registrations and a reservation held by an APTPL registrant
	// survive; the non-persistent registrant vanishes and the generation value
	// is preserved.
	m = NewMachine(admin)
	register(t, m, portA, keyA, true)
	register(t, m, portB, keyB, false)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})
	genBefore := m.Generation()
	m.PowerLoss()

	if !m.Registered(portA) {
		t.Fatal("APTPL registration lost on power loss")
	}
	if m.Registered(portB) {
		t.Fatal("non-APTPL registration survived power loss")
	}
	if m.Generation() != genBefore {
		t.Fatalf("generation = %d, want preserved %d", m.Generation(), genBefore)
	}
	resp := mustDispatch(t, m, Request{Op: OpReadReservation, Source: portA})
	if resp.Reservation == nil || resp.Reservation.Holder != portA {
		t.Fatalf("reservation = %v, want holder %v", resp.Reservation, portA)
	}

	// Reservation held by a non-APTPL holder is dropped even though another
	// APTPL registration survives.
	m = NewMachine(admin)
	register(t, m, portA, keyA, false)
	register(t, m, portB, keyB, true)
	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})
	m.PowerLoss()
	resp = mustDispatch(t, m, Request{Op: OpReadReservation, Source: portB})
	if resp.Reservation != nil {
		t.Fatalf("reservation survived loss of holder: %v", resp.Reservation)
	}
	if !m.Registered(portB) {
		t.Fatal("unrelated APTPL registration should survive")
	}
}

func TestGenerationBumpsOncePerStateChange(t *testing.T) {
	m := NewMachine(admin)
	checks := []struct {
		name string
		req  Request
		want uint64
	}{
		{"register A", Request{Op: OpRegister, Source: portA, NewKey: keyA}, 1},
		{"register B", Request{Op: OpRegister, Source: portB, NewKey: keyB}, 2},
		{"reserve", Request{Op: OpReserve, Source: portA, Key: keyA}, 3},
		{"idempotent reserve", Request{Op: OpReserve, Source: portA, Key: keyA}, 3},
		{"no-op release by other registrant", Request{Op: OpRelease, Source: portB, Key: keyB}, 3},
		{"read keys does not bump", Request{Op: OpReadKeys, Source: portA}, 3},
		{"key rotation", Request{Op: OpRegister, Source: portA, Key: keyA, NewKey: 0xAAAA}, 4},
		{"release by holder", Request{Op: OpRelease, Source: portA, Key: 0xAAAA}, 5},
		{"unregister", Request{Op: OpRegister, Source: portA, Key: 0xAAAA, NewKey: 0}, 6},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := m.Dispatch(tc.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Generation != tc.want {
				t.Fatalf("generation = %d, want %d", resp.Generation, tc.want)
			}
			if m.Generation() != tc.want {
				t.Fatalf("machine generation = %d, want %d", m.Generation(), tc.want)
			}
		})
	}
}

func TestWEROWriteAccessAndReservationConflict(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)
	register(t, m, portB, keyB, false)

	// Before any reservation every port can write.
	if !m.WriteAllowed(portC) {
		t.Fatal("unreserved LU should allow writes from any port")
	}

	mustDispatch(t, m, Request{Op: OpReserve, Source: portA, Key: keyA})
	if !m.WriteAllowed(portA) || !m.WriteAllowed(portB) {
		t.Fatal("registrants must retain write access under WERO")
	}
	if m.WriteAllowed(portC) {
		t.Fatal("unregistered port must be blocked under WERO")
	}

	// Another registrant cannot take the reservation without preempting.
	_, err := m.Dispatch(Request{Op: OpReserve, Source: portB, Key: keyB})
	wantError(t, err, CodeReservationConflict, ErrReservationConflict)
}

func TestSelfPreemptAndUnknownVictimRejected(t *testing.T) {
	m := NewMachine(admin)
	register(t, m, portA, keyA, false)

	_, err := m.Dispatch(Request{Op: OpPreempt, Source: portA, Key: keyA, PreemptKey: keyA})
	wantError(t, err, CodeInvalidRequest, ErrInvalidRequest)

	_, err = m.Dispatch(Request{Op: OpPreempt, Source: portA, Key: keyA, PreemptKey: keyB})
	wantError(t, err, CodeInvalidRequest, ErrInvalidRequest)

	if m.Generation() != 1 {
		t.Fatalf("generation = %d, failed actions must not bump it", m.Generation())
	}
}

func TestOpcodeString(t *testing.T) {
	cases := map[Opcode]string{
		OpRegister:        "REGISTER",
		OpReserve:         "RESERVE",
		OpRelease:         "RELEASE",
		OpPreempt:         "PREEMPT",
		OpPreemptAndAbort: "PREEMPT AND ABORT",
		OpClear:           "CLEAR",
		OpReadKeys:        "READ KEYS",
		OpReadReservation: "READ RESERVATION",
		Opcode(99):        "UNKNOWN",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Fatalf("Opcode(%d).String() = %q, want %q", int(op), got, want)
		}
	}
}
