package netsec

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSCOEncoding(t *testing.T) {
	tests := []struct {
		value     bool
		selectBit bool
		want      uint8
	}{
		{false, false, 0x00},
		{true, false, 0x01},
		{false, true, 0x80},
		{true, true, 0x81},
	}
	for _, tt := range tests {
		if got := EncodeSCO(tt.value, tt.selectBit); got != tt.want {
			t.Fatalf("EncodeSCO(%v, %v) = %#02x, want %#02x", tt.value, tt.selectBit, got, tt.want)
		}
		value, selectBit := DecodeSCO(tt.want)
		if value != tt.value || selectBit != tt.selectBit {
			t.Fatalf("DecodeSCO(%#02x) = (%v, %v), want (%v, %v)", tt.want, value, selectBit, tt.value, tt.selectBit)
		}
		command := ControlCommand{Value: tt.value, Select: tt.selectBit}
		if got := command.SCO(); got != tt.want {
			t.Fatalf("ControlCommand.SCO() = %#02x, want %#02x", got, tt.want)
		}
	}
}

func TestSelectThenExecuteOnSameConnectionAndIOA(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	selectResult := state.HandleSCO("conn-a", 6, 100, 0x81)
	if !selectResult.Allowed || selectResult.Reason != ReasonSelectAccepted {
		t.Fatalf("SELECT result = %+v, want accepted SELECT", selectResult)
	}

	executeResult := state.HandleSCO("conn-a", 6, 100, 0x01)
	if !executeResult.Allowed || executeResult.Reason != ReasonExecuteAccepted {
		t.Fatalf("EXECUTE result = %+v, want accepted EXECUTE", executeResult)
	}

	replayResult := state.HandleSCO("conn-a", 6, 100, 0x01)
	if replayResult.Allowed {
		t.Fatalf("replayed EXECUTE = %+v, want rejection", replayResult)
	}
}

func TestExecuteMustUseSelectedValue(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT result = %+v, want accepted", got)
	}
	got := state.Execute("conn-a", 100, false)
	if got.Allowed || got.Reason != ReasonCommandValueMismatch {
		t.Fatalf("mismatched EXECUTE = %+v, want value-mismatch rejection", got)
	}

	if got := state.Execute("conn-a", 100, true); !got.Allowed {
		t.Fatalf("matching EXECUTE after mismatch = %+v, want accepted", got)
	}
}

func TestReselectReplacesSelectedValue(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("first SELECT = %+v, want accepted", got)
	}
	if got := state.Select("conn-a", 100, false); !got.Allowed {
		t.Fatalf("second SELECT = %+v, want accepted", got)
	}

	if got := state.Execute("conn-a", 100, true); got.Allowed || got.Reason != ReasonCommandValueMismatch {
		t.Fatalf("EXECUTE of replaced value = %+v, want value-mismatch rejection", got)
	}
	if got := state.Execute("conn-a", 100, false); !got.Allowed {
		t.Fatalf("EXECUTE of current value = %+v, want accepted", got)
	}
}

func TestSelectBeforeOperateRejectsDirectExecute(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	got := state.Execute("conn-a", 100, true)
	if got.Allowed || got.Reason != ReasonSelectRequired {
		t.Fatalf("direct EXECUTE = %+v, want select-required rejection", got)
	}
}

func TestDirectExecuteAllowedWithoutSelectConfiguration(t *testing.T) {
	state := New()

	got := state.Execute("conn-a", 100, true)
	if !got.Allowed || got.Reason != ReasonDirectExecuteAccepted {
		t.Fatalf("direct EXECUTE = %+v, want accepted", got)
	}
}

func TestExecuteFromAnotherConnectionCannotUseSelection(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT on conn-a = %+v, want accepted", got)
	}

	attack := state.Execute("conn-b", 100, true)
	if attack.Allowed {
		t.Fatalf("cross-connection EXECUTE = %+v, want rejection", attack)
	}

	legitimate := state.Execute("conn-a", 100, true)
	if !legitimate.Allowed {
		t.Fatalf("legitimate EXECUTE after cross-connection attempt = %+v, want accepted", legitimate)
	}
}

func TestExecuteForAnotherIOACannotUseSelection(t *testing.T) {
	state := New(WithSelectBeforeOperate(100), WithSelectBeforeOperate(101))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT on IOA 100 = %+v, want accepted", got)
	}

	attack := state.Execute("conn-a", 101, true)
	if attack.Allowed {
		t.Fatalf("wrong-IOA EXECUTE = %+v, want rejection", attack)
	}

	legitimate := state.Execute("conn-a", 100, true)
	if !legitimate.Allowed {
		t.Fatalf("legitimate EXECUTE after wrong-IOA attempt = %+v, want accepted", legitimate)
	}
}

func TestSelectionExpiresAfterThirtySeconds(t *testing.T) {
	clock := newTestClock(time.Unix(1_000_000, 0))
	state := New(WithClock(clock.Now), WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}

	clock.Advance(29 * time.Second)
	if got := state.Execute("conn-a", 100, true); !got.Allowed {
		t.Fatalf("EXECUTE after 29s = %+v, want accepted", got)
	}

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("second SELECT = %+v, want accepted", got)
	}
	clock.Advance(DefaultSelectTimeout)
	got := state.Execute("conn-a", 100, true)
	if got.Allowed || got.Reason != ReasonSelectionExpired {
		t.Fatalf("EXECUTE at 30s = %+v, want expiry rejection", got)
	}

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT after expiry = %+v, want accepted", got)
	}
	clock.Advance(DefaultSelectTimeout - time.Nanosecond)
	if got := state.Execute("conn-a", 100, true); !got.Allowed {
		t.Fatalf("EXECUTE before new deadline = %+v, want accepted", got)
	}
}

func TestCancelClearsSelection(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}
	if got := state.Cancel("conn-a", 100); !got.Allowed || got.Reason != ReasonCancelAccepted {
		t.Fatalf("CANCEL = %+v, want accepted", got)
	}
	if got := state.Execute("conn-a", 100, true); got.Allowed {
		t.Fatalf("EXECUTE after CANCEL = %+v, want rejection", got)
	}
}

func TestCancelIsBoundToConnectionAndIOA(t *testing.T) {
	state := New(WithSelectBeforeOperate(100), WithSelectBeforeOperate(101))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}
	if got := state.Cancel("conn-b", 100); !got.Allowed {
		t.Fatalf("CANCEL from conn-b = %+v, want harmless acceptance", got)
	}
	if got := state.Cancel("conn-a", 101); !got.Allowed {
		t.Fatalf("CANCEL for wrong IOA = %+v, want harmless acceptance", got)
	}
	if got := state.Execute("conn-a", 100, true); !got.Allowed {
		t.Fatalf("EXECUTE after unrelated CANCELs = %+v, want accepted", got)
	}
}

func TestUnsupportedTypeIsRejected(t *testing.T) {
	state := New()

	got := state.HandleCommand("conn-a", ControlCommand{
		TypeID: 46,
		COT:    6,
		IOA:    100,
		Value:  true,
		Select: false,
	})
	if got.Allowed || got.Reason != ReasonUnsupportedType {
		t.Fatalf("unsupported type result = %+v, want rejection", got)
	}
}

func TestCloseConnectionDiscardsSelections(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}
	state.CloseConnection("conn-a")
	if got := state.Execute("conn-a", 100, true); got.Allowed {
		t.Fatalf("EXECUTE after connection close = %+v, want rejection", got)
	}
}

func TestPointConfigurationAndResultHelpers(t *testing.T) {
	state := NewCommandState()

	if got := state.PointMode(100); got != PointDirectExecute {
		t.Fatalf("default PointMode(100) = %v, want %v", got, PointDirectExecute)
	}
	state.SetPointMode(100, PointSelectBeforeOperate)
	if got := state.PointMode(100); got != PointSelectBeforeOperate {
		t.Fatalf("PointMode(100) after SetPointMode = %v, want %v", got, PointSelectBeforeOperate)
	}

	rejected := state.Execute("conn-a", 100, true)
	if rejected.Accepted() || rejected.OK() || !rejected.Rejected() {
		t.Fatalf("Result helpers disagree with rejected result: %+v", rejected)
	}
}

func TestExplicitTimeAPIs(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))
	start := time.Unix(2_000_000, 0)

	if got := state.SelectAt("conn-a", 100, true, start); !got.Allowed {
		t.Fatalf("SelectAt = %+v, want accepted", got)
	}
	if got := state.ExecuteAt("conn-a", 100, true, start.Add(DefaultSelectTimeout-time.Nanosecond)); !got.Allowed {
		t.Fatalf("ExecuteAt before deadline = %+v, want accepted", got)
	}

	if got := state.SelectAt("conn-a", 100, true, start); !got.Allowed {
		t.Fatalf("second SelectAt = %+v, want accepted", got)
	}
	if got := state.ExecuteAt("conn-a", 100, true, start.Add(DefaultSelectTimeout)); got.Allowed || got.Reason != ReasonSelectionExpired {
		t.Fatalf("ExecuteAt at deadline = %+v, want expiry rejection", got)
	}

	if got := state.SelectAt("conn-a", 100, true, start); !got.Allowed {
		t.Fatalf("third SelectAt = %+v, want accepted", got)
	}
	if got := state.CancelAt("conn-a", 100, start.Add(time.Second)); !got.Allowed || got.Reason != ReasonCancelAccepted {
		t.Fatalf("CancelAt = %+v, want accepted cancellation", got)
	}
	if got := state.ExecuteAt("conn-a", 100, true, start.Add(2*time.Second)); got.Allowed {
		t.Fatalf("ExecuteAt after CancelAt = %+v, want rejection", got)
	}
}

func TestCustomSelectTimeout(t *testing.T) {
	clock := newTestClock(time.Unix(3_000_000, 0))
	state := New(WithClock(clock.Now), WithSelectTimeout(5*time.Second), WithSelectBeforeOperate(100))

	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}
	clock.Advance(5 * time.Second)
	if got := state.Execute("conn-a", 100, true); got.Allowed || got.Reason != ReasonSelectionExpired {
		t.Fatalf("EXECUTE after custom timeout = %+v, want expiry rejection", got)
	}
}

func TestConcurrentExecuteConsumesSelectionOnlyOnce(t *testing.T) {
	state := New(WithSelectBeforeOperate(100))
	if got := state.Select("conn-a", 100, true); !got.Allowed {
		t.Fatalf("SELECT = %+v, want accepted", got)
	}

	const goroutines = 32
	var accepted atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if state.Execute("conn-a", 100, true).Allowed {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted EXECUTEs = %d, want exactly 1", got)
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock {
	return &testClock{now: now}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
