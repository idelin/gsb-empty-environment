package netsec

import (
	"errors"
	"testing"
)

func drive(t *testing.T, m *Machine, evs ...Event) {
	t.Helper()
	for _, ev := range evs {
		if err := m.Handle(ev); err != nil {
			t.Fatalf("Handle(%s) returned unexpected error: %v", ev, err)
		}
	}
}

func TestInitialClientHelloAccepted(t *testing.T) {
	m := New()
	if err := m.Handle(EventClientHello); err != nil {
		t.Fatalf("first ClientHello rejected: %v", err)
	}
	if m.State() != StateHandshaking {
		t.Fatalf("state = %s, want %s", m.State(), StateHandshaking)
	}
}

func TestHandshakeCompletes(t *testing.T) {
	m := New()
	drive(t, m, EventClientHello, EventServerHello, EventFinished)
	if m.State() != StateEstablished {
		t.Fatalf("state = %s, want %s", m.State(), StateEstablished)
	}
	drive(t, m, EventApplicationData)
}

func TestClientHelloAfterFinishedFails(t *testing.T) {
	m := New()
	drive(t, m, EventClientHello, EventServerHello, EventFinished)

	err := m.Handle(EventClientHello)
	if !errors.Is(err, ErrRenegotiation) {
		t.Fatalf("post-Finished ClientHello error = %v, want %v", err, ErrRenegotiation)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want %s", m.State(), StateFailed)
	}
}

func TestNoNestedHandshakeAfterFailure(t *testing.T) {
	m := New()
	drive(t, m, EventClientHello, EventServerHello, EventFinished)
	if err := m.Handle(EventClientHello); !errors.Is(err, ErrRenegotiation) {
		t.Fatalf("post-Finished ClientHello error = %v, want %v", err, ErrRenegotiation)
	}

	// A further ClientHello must not restart a handshake.
	if err := m.Handle(EventClientHello); !errors.Is(err, ErrRenegotiation) {
		t.Fatalf("ClientHello after failure error = %v, want %v", err, ErrRenegotiation)
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want %s (nested handshake must not begin)", m.State(), StateFailed)
	}
	if !errors.Is(m.Err(), ErrRenegotiation) {
		t.Fatalf("Err() = %v, want %v", m.Err(), ErrRenegotiation)
	}
}

func TestHelloRetryRequestSecondClientHelloAllowed(t *testing.T) {
	m := New()
	drive(t, m, EventClientHello, EventServerHello, EventClientHello, EventServerHello, EventFinished)
	if m.State() != StateEstablished {
		t.Fatalf("state = %s, want %s", m.State(), StateEstablished)
	}
}

func TestUnexpectedEventBeforeHandshakeFails(t *testing.T) {
	m := New()
	if err := m.Handle(EventApplicationData); err == nil {
		t.Fatal("ApplicationData before ClientHello accepted, want error")
	}
	if m.State() != StateFailed {
		t.Fatalf("state = %s, want %s", m.State(), StateFailed)
	}
}
