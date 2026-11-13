package netsec

import (
	"errors"
	"testing"
	"time"
)

const testWindow = 5 * time.Second

func newTestMachine(t *testing.T) *Machine {
	t.Helper()
	m := New(testWindow)
	if m.State() != StateInit {
		t.Fatalf("new machine state = %s, want %s", m.State(), StateInit)
	}
	return m
}

func TestRetryThenEarlyDataRejected(t *testing.T) {
	m := newTestMachine(t)

	if err := m.OnClientHello(time.Second); err != nil {
		t.Fatalf("OnClientHello: %v", err)
	}
	if err := m.OnHelloRetryRequest(); err != nil {
		t.Fatalf("OnHelloRetryRequest: %v", err)
	}
	if m.State() != StateRetrySent {
		t.Fatalf("state after retry = %s, want %s", m.State(), StateRetrySent)
	}

	dec, err := m.OnEarlyData()
	if err != nil {
		t.Fatalf("OnEarlyData: %v", err)
	}
	if dec != DecisionRejectEarlyData {
		t.Fatalf("decision = %s, want %s", dec, DecisionRejectEarlyData)
	}
	if m.State() != StateEarlyDataRejected {
		t.Fatalf("state = %s, want %s", m.State(), StateEarlyDataRejected)
	}
}

func TestStaleTicketRejected(t *testing.T) {
	for _, skew := range []time.Duration{testWindow + time.Second, -(testWindow + time.Second)} {
		m := newTestMachine(t)
		if err := m.OnClientHello(skew); err != nil {
			t.Fatalf("OnClientHello(%s): %v", skew, err)
		}
		dec, err := m.OnEarlyData()
		if err != nil {
			t.Fatalf("OnEarlyData: %v", err)
		}
		if dec != DecisionRejectEarlyData {
			t.Fatalf("skew %s: decision = %s, want %s", skew, dec, DecisionRejectEarlyData)
		}
		if m.State() != StateEarlyDataRejected {
			t.Fatalf("skew %s: state = %s, want %s", skew, m.State(), StateEarlyDataRejected)
		}
	}
}

func TestEarlyDataAcceptedWithinWindow(t *testing.T) {
	for _, skew := range []time.Duration{0, testWindow, -testWindow} {
		m := newTestMachine(t)
		if err := m.OnClientHello(skew); err != nil {
			t.Fatalf("OnClientHello(%s): %v", skew, err)
		}
		dec, err := m.OnEarlyData()
		if err != nil {
			t.Fatalf("OnEarlyData: %v", err)
		}
		if dec != DecisionAcceptEarlyData {
			t.Fatalf("skew %s: decision = %s, want %s", skew, dec, DecisionAcceptEarlyData)
		}
		if m.State() != StateEarlyDataAccepted {
			t.Fatalf("skew %s: state = %s, want %s", skew, m.State(), StateEarlyDataAccepted)
		}
	}
}

func TestDefaultWindow(t *testing.T) {
	m := New(0)
	if err := m.OnClientHello(DefaultMaxTicketAgeSkew + time.Second); err != nil {
		t.Fatalf("OnClientHello: %v", err)
	}
	dec, err := m.OnEarlyData()
	if err != nil {
		t.Fatalf("OnEarlyData: %v", err)
	}
	if dec != DecisionRejectEarlyData {
		t.Fatalf("decision = %s, want %s", dec, DecisionRejectEarlyData)
	}
}

func TestUnexpectedEvents(t *testing.T) {
	t.Run("EarlyDataBeforeClientHello", func(t *testing.T) {
		m := newTestMachine(t)
		if _, err := m.OnEarlyData(); !errors.Is(err, ErrUnexpectedEvent) {
			t.Fatalf("err = %v, want %v", err, ErrUnexpectedEvent)
		}
	})
	t.Run("RetryBeforeClientHello", func(t *testing.T) {
		m := newTestMachine(t)
		if err := m.OnHelloRetryRequest(); !errors.Is(err, ErrUnexpectedEvent) {
			t.Fatalf("err = %v, want %v", err, ErrUnexpectedEvent)
		}
	})
	t.Run("DuplicateClientHello", func(t *testing.T) {
		m := newTestMachine(t)
		if err := m.OnClientHello(0); err != nil {
			t.Fatalf("OnClientHello: %v", err)
		}
		if err := m.OnClientHello(0); !errors.Is(err, ErrUnexpectedEvent) {
			t.Fatalf("err = %v, want %v", err, ErrUnexpectedEvent)
		}
	})
	t.Run("DuplicateRetry", func(t *testing.T) {
		m := newTestMachine(t)
		if err := m.OnClientHello(0); err != nil {
			t.Fatalf("OnClientHello: %v", err)
		}
		if err := m.OnHelloRetryRequest(); err != nil {
			t.Fatalf("OnHelloRetryRequest: %v", err)
		}
		if err := m.OnHelloRetryRequest(); !errors.Is(err, ErrUnexpectedEvent) {
			t.Fatalf("err = %v, want %v", err, ErrUnexpectedEvent)
		}
	})
	t.Run("DuplicateEarlyData", func(t *testing.T) {
		m := newTestMachine(t)
		if err := m.OnClientHello(0); err != nil {
			t.Fatalf("OnClientHello: %v", err)
		}
		if _, err := m.OnEarlyData(); err != nil {
			t.Fatalf("OnEarlyData: %v", err)
		}
		if _, err := m.OnEarlyData(); !errors.Is(err, ErrUnexpectedEvent) {
			t.Fatalf("err = %v, want %v", err, ErrUnexpectedEvent)
		}
	})
}
