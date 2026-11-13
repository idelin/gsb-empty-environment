package netsec

import (
	"errors"
	"testing"
)

func feed(t *testing.T, m *Machine, states ...State) {
	t.Helper()
	for _, s := range states {
		if err := m.Advance(s); err != nil {
			t.Fatalf("Advance(%v) returned unexpected error: %v", s, err)
		}
	}
}

func TestSingleRetryHandshakeAccepted(t *testing.T) {
	m := New()
	feed(t, m,
		StateClientHello,
		StateHelloRetryRequest,
		StateClientHello,
		StateServerHello,
		StateFinished,
	)
	if m.State() != StateFinished {
		t.Fatalf("state = %v, want StateFinished", m.State())
	}
}

func TestSecondRetryAfterLegalRetryFails(t *testing.T) {
	m := New()
	feed(t, m,
		StateClientHello,
		StateHelloRetryRequest,
		StateClientHello,
	)
	if err := m.Advance(StateHelloRetryRequest); !errors.Is(err, ErrSecondHelloRetryRequest) {
		t.Fatalf("second HelloRetryRequest: err = %v, want %v", err, ErrSecondHelloRetryRequest)
	}
}

func TestHandshakeWithoutRetryAccepted(t *testing.T) {
	m := New()
	feed(t, m, StateClientHello, StateServerHello, StateFinished)
}
