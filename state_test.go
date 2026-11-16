package netsec

import (
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestMachine(clk *fakeClock) *StateMachine {
	m := NewStateMachine(WithClock(clk.now), RejectUnknownPoints())
	m.AddPoint(100, true)  // select-before-execute point
	m.AddPoint(101, true)  // second select-first point, used for wrong-IOA case
	m.AddPoint(200, false) // direct-execute point
	return m
}

func conn(name string) ConnID { return ConnID(name) }

func selectCmd(ioa IOA, on bool) SingleCommand {
	return SingleCommand{IOA: ioa, Select: true, On: on}
}

func execCmd(ioa IOA, on bool) SingleCommand {
	return SingleCommand{IOA: ioa, Select: false, On: on}
}

func feed(t *testing.T, m *StateMachine, c ConnID, cause COT, cmd SingleCommand) Decision {
	t.Helper()
	d := m.HandleSingleCommand(c, cause, cmd)
	return d
}

func assertReject(t *testing.T, d Decision, want error) {
	t.Helper()
	if !d.Rejected() {
		t.Fatalf("got accepted %v, want reject %v", d.Kind, want)
	}
	if !errors.Is(d.Reason, want) {
		t.Fatalf("reason = %v, want %v", d.Reason, want)
	}
}

func assertKind(t *testing.T, d Decision, want Kind) {
	t.Helper()
	if d.Kind != want {
		t.Fatalf("kind = %v, want %v (reason=%v)", d.Kind, want, d.Reason)
	}
}

func TestSelectThenExecuteSameConnection(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	sel, ok := m.ActiveSelection(100)
	if !ok || sel.Conn != conn("A") {
		t.Fatalf("active selection = %+v ok=%v, want conn A", sel, ok)
	}

	// Still inside the 30 second window.
	clk.advance(29 * time.Second)
	d := feed(t, m, conn("A"), COTActivation, execCmd(100, true))
	assertKind(t, d, Executed)

	// Selection is consumed and cannot be replayed.
	if _, ok := m.ActiveSelection(100); ok {
		t.Fatal("selection should be cleared after execute")
	}
	assertReject(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), ErrSelectRequired)
}

func TestCrossConnectionExecuteRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	// Attacker on another connection tries to execute the armed point.
	d := feed(t, m, conn("attacker"), COTActivation, execCmd(100, true))
	assertReject(t, d, ErrConnectionMismatch)

	// The original selection must remain intact for its owner.
	sel, ok := m.ActiveSelection(100)
	if !ok || sel.Conn != conn("A") {
		t.Fatalf("selection was disturbed by cross-connection execute: %+v ok=%v", sel, ok)
	}

	// Cross-connection select is likewise refused while A holds the point.
	assertReject(t, feed(t, m, conn("attacker"), COTActivation, selectCmd(100, false)), ErrConnectionMismatch)
}

func TestCrossConnectionCancelRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)
	assertReject(t, feed(t, m, conn("attacker"), COTCancellation, selectCmd(100, true)), ErrConnectionMismatch)

	if _, ok := m.ActiveSelection(100); !ok {
		t.Fatal("cross-connection cancel must not clear A's selection")
	}
}

func TestWrongIOAExecuteRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	// Select on IOA 100 ...
	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	// ... then try to execute a different IOA from the same connection.
	d := feed(t, m, conn("A"), COTActivation, execCmd(101, true))
	assertReject(t, d, ErrSelectRequired)

	// The selection on 100 is untouched and still executable.
	assertKind(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), Executed)
}

func TestSelectionTimeoutVoidsExecute(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	clk.advance(DefaultSelectTimeout + time.Second)
	d := feed(t, m, conn("A"), COTActivation, execCmd(100, true))
	assertReject(t, d, ErrSelectionExpired)

	if _, ok := m.ActiveSelection(100); ok {
		t.Fatal("expired selection should be reported as inactive")
	}

	// After timeout the point can be re-armed and executed normally.
	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)
	assertKind(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), Executed)
}

func TestSweepRemovesExpiredSelections(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)
	clk.advance(DefaultSelectTimeout + time.Second)
	m.Sweep()

	if _, ok := m.ActiveSelection(100); ok {
		t.Fatal("Sweep must remove the expired selection")
	}
}

func TestDirectExecuteRejectedOnSelectFirstPoint(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	// No select at all: direct execute must be refused.
	assertReject(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), ErrSelectRequired)
}

func TestDirectExecuteAllowedOnDirectPoint(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, execCmd(200, true)), Executed)
}

func TestCancellationClearsSelection(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	d := feed(t, m, conn("A"), COTCancellation, selectCmd(100, true))
	assertKind(t, d, Cancelled)

	if _, ok := m.ActiveSelection(100); ok {
		t.Fatal("cancel must clear the selection")
	}
	// A subsequent execute now has nothing to confirm and is rejected.
	assertReject(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), ErrSelectRequired)
}

func TestCommandValueMismatchRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertKind(t, feed(t, m, conn("A"), COTActivation, selectCmd(100, true)), Selected)

	// Selected ON, execute OFF: different command value.
	assertReject(t, feed(t, m, conn("A"), COTActivation, execCmd(100, false)), ErrValueMismatch)

	// Selection survives the failed attempt and the correct value still works.
	assertKind(t, feed(t, m, conn("A"), COTActivation, execCmd(100, true)), Executed)
}

func TestUnknownPointRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	assertReject(t, feed(t, m, conn("A"), COTActivation, selectCmd(999, true)), ErrUnknownPoint)
	assertReject(t, feed(t, m, conn("A"), COTActivation, execCmd(999, true)), ErrUnknownPoint)
}

func TestUnsupportedTypeRejected(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	d, err := m.HandleFrame(Frame{Type: 46, Cause: COTActivation, Conn: conn("A"), Object: []byte{1, 0, 0, 0x80}})
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("err = %v, want ErrUnsupportedType", err)
	}
	assertReject(t, d, ErrUnsupportedType)
}

func TestHandleFrameMalformedObject(t *testing.T) {
	m := NewStateMachine()
	_, err := m.HandleFrame(Frame{Type: TypeSingleCommand, Conn: conn("A"), Object: []byte{1}})
	if !errors.Is(err, ErrMalformedSingleCommand) {
		t.Fatalf("err = %v, want ErrMalformedSingleCommand", err)
	}
}

func TestConcurrentConnections(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	m := newTestMachine(clk)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := ConnID("conn-" + string(rune('A'+n%4)))
			_ = m.HandleSingleCommand(c, COTActivation, selectCmd(100, true))
			_ = m.HandleSingleCommand(c, COTActivation, execCmd(100, true))
			m.ActiveSelection(100)
			m.Sweep()
		}(i)
	}
	wg.Wait()
}

func TestDefaultTimeoutIsThirtySeconds(t *testing.T) {
	if DefaultSelectTimeout != 30*time.Second {
		t.Fatalf("DefaultSelectTimeout = %v, want 30s", DefaultSelectTimeout)
	}
}
