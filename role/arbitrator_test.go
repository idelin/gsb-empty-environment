package role

import (
	"errors"
	"sync"
	"testing"
)

const (
	c1 ControllerID = "ctrl-1"
	c2 ControllerID = "ctrl-2"
	c3 ControllerID = "ctrl-3"
)

func roleError(t *testing.T, err error) *RoleError {
	t.Helper()
	var re *RoleError
	if !errors.As(err, &re) {
		t.Fatalf("want *RoleError, got %v", err)
	}
	return re
}

func newConnected(t *testing.T, ids ...ControllerID) *Arbitrator {
	t.Helper()
	a := NewArbitrator()
	for _, id := range ids {
		if err := a.Connect(id); err != nil {
			t.Fatalf("Connect(%q): %v", id, err)
		}
	}
	return a
}

// First ROLE_REQUEST on a connection is accepted regardless of the value
// carried in its generation id.
func TestFirstRequestAlwaysAccepted(t *testing.T) {
	a := newConnected(t, c1)
	d, err := a.HandleRoleRequest(c1, RoleMaster, 42)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if d.Role != RoleMaster || d.Generation != 42 {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if got := a.Master(); got != c1 {
		t.Fatalf("master = %q, want %q", got, c1)
	}
}

// Generation ids are compared by signed 64-bit difference so a counter
// that wraps through zero still orders requests correctly.
func TestGenerationWraparound(t *testing.T) {
	cases := []struct {
		name   string
		stored int64
		req    int64
		stale  bool
	}{
		{"normal newer", 100, 101, false},
		{"normal equal", 100, 100, false},
		{"normal older", 100, 99, true},
		{"wrap newer across zero", -1, 0, false},
		{"wrap older across zero", 0, -1, true},
		{"wrap newer at max boundary", 1<<63 - 1, -1 << 63, false},
		{"wrap older at min boundary", -1 << 63, 1<<63 - 1, true},
		{"wrap far forward", -1 << 62, 1 << 62, false},
		// Distances must stay within the signed half-range (-2^63, 2^63);
		// points exactly 2^63 apart are indistinguishable by subtraction.
		{"wrap far backward", 1<<62 - 1, -1 << 62, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newConnected(t, c1)
			if _, err := a.HandleRoleRequest(c1, RoleEqual, tc.stored); err != nil {
				t.Fatalf("seed request: %v", err)
			}
			_, err := a.HandleRoleRequest(c1, RoleNochange, tc.req)
			if tc.stale {
				re := roleError(t, err)
				if re.Code != ErrCodeStale {
					t.Fatalf("code = %d, want STALE(%d)", re.Code, ErrCodeStale)
				}
				if gen, _ := a.Generation(c1); gen != tc.stored {
					t.Fatalf("stored generation changed to %d, want %d", gen, tc.stored)
				}
				if role, _ := a.Role(c1); role != RoleEqual {
					t.Fatalf("stale request changed role to %s", role)
				}
			} else if err != nil {
				t.Fatalf("expected acceptance, got %v", err)
			}
		})
	}
}

// A stale ROLE_REQUEST must never change role or generation id.
func TestStaleRequestLeavesStateUntouched(t *testing.T) {
	a := newConnected(t, c1)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 10); err != nil {
		t.Fatal(err)
	}
	var err error
	_, err = a.HandleRoleRequest(c1, RoleSlave, 9)
	re := roleError(t, err)
	if re.Code != ErrCodeStale {
		t.Fatalf("code = %d, want STALE", re.Code)
	}
	if role, _ := a.Role(c1); role != RoleMaster {
		t.Fatalf("role = %s, want MASTER", role)
	}
	if gen, _ := a.Generation(c1); gen != 10 {
		t.Fatalf("generation = %d, want 10", gen)
	}
}

// Unknown and unsupported role values are rejected as protocol errors
// rather than stale errors.
func TestUnsupportedRole(t *testing.T) {
	a := newConnected(t, c1)
	_, err := a.HandleRoleRequest(c1, Role(99), 1)
	re := roleError(t, err)
	if re.Code != ErrCodeUnsupported {
		t.Fatalf("code = %d, want UNSUPPORTED(%d)", re.Code, ErrCodeUnsupported)
	}
}

func TestUnknownController(t *testing.T) {
	a := NewArbitrator()
	if _, err := a.HandleRoleRequest(c1, RoleEqual, 0); !errors.Is(err, ErrUnknownController) {
		t.Fatalf("HandleRoleRequest err = %v, want ErrUnknownController", err)
	}
	if err := a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod}); !errors.Is(err, ErrUnknownController) {
		t.Fatalf("Enqueue err = %v, want ErrUnknownController", err)
	}
	if err := a.Connect(c1); err != nil {
		t.Fatal(err)
	}
	if err := a.Connect(c1); !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("duplicate Connect err = %v, want ErrAlreadyConnected", err)
	}
}

// Old-master race: a new master request demotes the current master to
// slave, and the demoted controller's queued writes are invalidated in
// queue order while its barrier and read messages survive.
func TestOldMasterRaceAndBarrierOrder(t *testing.T) {
	a := newConnected(t, c1, c2)

	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}

	// The old master queues work before the role change lands.
	oldWork := []*Envelope{
		{MsgType: TypeFlowMod, Xid: 1, Payload: []byte("flow-1")},
		{MsgType: TypePacketOut, Xid: 2, Payload: []byte("pout-1")},
		{MsgType: 20, Xid: 3, Payload: []byte("barrier-req")}, // OFPT_BARRIER_REQUEST
		{MsgType: TypeMeterMod, Xid: 4, Payload: []byte("meter-1")},
		{MsgType: 4, Xid: 5, Payload: []byte("stats-req")}, // OFPT_MULTIPART_REQUEST
	}
	for _, env := range oldWork {
		if err := a.Enqueue(c1, env); err != nil {
			t.Fatalf("enqueue old-master work: %v", err)
		}
	}

	// A second controller takes over master; c1 is demoted and its
	// pre-transition writes are invalidated.
	decision, err := a.HandleRoleRequest(c2, RoleMaster, 7)
	if err != nil {
		t.Fatalf("new master request: %v", err)
	}
	if role, _ := a.Role(c1); role != RoleSlave {
		t.Fatalf("c1 role = %s, want SLAVE", role)
	}
	if a.Master() != c2 {
		t.Fatalf("master = %q, want %q", a.Master(), c2)
	}
	if len(decision.Demoted) != 1 || decision.Demoted[0] != c1 {
		t.Fatalf("Demoted = %v, want [%q]", decision.Demoted, c1)
	}

	// Anything queued after the demotion is subject to slave admission.
	err = a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod, Xid: 6})
	if err == nil {
		t.Fatal("slave FLOW_MOD after demotion was accepted")
	}
	if re := roleError(t, err); re.Code != ErrCodeBadRole {
		t.Fatalf("slave write code = %d, want BAD_ROLE(%d)", re.Code, ErrCodeBadRole)
	}

	want := []struct {
		xid     uint32
		verdict Verdict
	}{
		{1, VerdictDrop},    // FLOW_MOD queued before demotion
		{2, VerdictDrop},    // PACKET_OUT queued before demotion
		{3, VerdictDeliver}, // barrier keeps the ordering boundary
		{4, VerdictDrop},    // METER_MOD queued before demotion
		{5, VerdictDeliver}, // read request stays allowed
	}
	for i, w := range want {
		env := a.Next(c1)
		if env == nil {
			t.Fatalf("message %d missing from queue", i)
		}
		if env.Xid != w.xid || env.Verdict != w.verdict {
			t.Fatalf("message %d: got (xid=%d verdict=%d), want (xid=%d verdict=%d)",
				i, env.Xid, env.Verdict, w.xid, w.verdict)
		}
	}
	if a.Next(c1) != nil {
		t.Fatal("queue should be drained")
	}

	// Reads queued by the now-slave still enter the queue and deliver.
	if err := a.Enqueue(c1, &Envelope{MsgType: 4, Xid: 7}); err != nil {
		t.Fatalf("slave read enqueue: %v", err)
	}
	if env := a.Next(c1); env == nil || env.Verdict != VerdictDeliver {
		t.Fatalf("post-demotion read verdict = %v, want DELIVER", env)
	}
}

// A master that demotes itself to slave invalidates its own queued writes.
func TestMasterSelfDemotion(t *testing.T) {
	a := newConnected(t, c1)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod, Xid: 1}); err != nil {
		t.Fatal(err)
	}
	decision, err := a.HandleRoleRequest(c1, RoleSlave, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Demoted) != 1 || decision.Demoted[0] != c1 {
		t.Fatalf("Demoted = %v, want [%q]", decision.Demoted, c1)
	}
	if a.Master() != "" {
		t.Fatalf("master = %q, want empty", a.Master())
	}
	env := a.Next(c1)
	if env == nil || env.Verdict != VerdictDrop {
		t.Fatalf("queued write verdict = %v, want DROP", env)
	}
}

// Re-requesting master with a fresh generation id refreshes state without
// spurious demotions.
func TestMasterRerequest(t *testing.T) {
	a := newConnected(t, c1)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}
	d, err := a.HandleRoleRequest(c1, RoleMaster, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Demoted) != 0 {
		t.Fatalf("Demoted = %v, want empty", d.Demoted)
	}
	if gen, _ := a.Generation(c1); gen != 2 {
		t.Fatalf("generation = %d, want 2", gen)
	}
}

// Master demoted to equal releases the master reservation and invalidates
// the queued pre-transition writes.
func TestMasterToEqual(t *testing.T) {
	a := newConnected(t, c1)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Enqueue(c1, &Envelope{MsgType: TypePacketOut, Xid: 1}); err != nil {
		t.Fatal(err)
	}
	d, err := a.HandleRoleRequest(c1, RoleEqual, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Demoted) != 1 || d.Role != RoleEqual {
		t.Fatalf("decision = %+v, want demotion to EQUAL", d)
	}
	if a.Master() != "" {
		t.Fatalf("master = %q, want empty", a.Master())
	}
	if env := a.Next(c1); env == nil || env.Verdict != VerdictDrop {
		t.Fatalf("old write verdict = %v, want DROP", env)
	}
	// Equal controllers write again immediately.
	if err := a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod}); err != nil {
		t.Fatalf("equal write: %v", err)
	}
}

// Slave controllers cannot issue FLOW_MOD, PACKET_OUT or METER_MOD, while
// read and statistics messages remain permitted.
func TestSlaveWriteRejection(t *testing.T) {
	a := newConnected(t, c1)
	if _, err := a.HandleRoleRequest(c1, RoleSlave, 1); err != nil {
		t.Fatal(err)
	}
	for _, mt := range []uint8{TypeFlowMod, TypePacketOut, TypeMeterMod, TypeGroupMod, TypePortMod, TypeTableMod} {
		err := a.Enqueue(c1, &Envelope{MsgType: mt, Xid: 1})
		re := roleError(t, err)
		if re.Code != ErrCodeBadRole {
			t.Fatalf("msgType %d: code = %d, want BAD_ROLE", mt, re.Code)
		}
	}
	// Reads (multipart request), barriers and role requests still pass.
	for _, mt := range []uint8{4, 20, TypeRoleRequest} {
		if err := a.Enqueue(c1, &Envelope{MsgType: mt, Xid: 2}); err != nil {
			t.Fatalf("msgType %d should be allowed: %v", mt, err)
		}
	}
	if a.Pending(c1) != 3 {
		t.Fatalf("pending = %d, want 3", a.Pending(c1))
	}
}

// Equal and master controllers retain full write access.
func TestMasterAndEqualWritesAllowed(t *testing.T) {
	a := newConnected(t, c1, c2)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}
	// c2 stays at its initial EQUAL role.
	for _, id := range []ControllerID{c1, c2} {
		for _, mt := range []uint8{TypeFlowMod, TypePacketOut, TypeMeterMod} {
			if err := a.Enqueue(id, &Envelope{MsgType: mt}); err != nil {
				t.Fatalf("%s msgType %d: %v", id, mt, err)
			}
		}
	}
}

// Asynchronous messages fan out only to master and equal controllers;
// slave controllers are excluded and the result is deterministically
// ordered.
func TestAsyncRecipients(t *testing.T) {
	a := newConnected(t, c1, c2, c3)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.HandleRoleRequest(c3, RoleSlave, 1); err != nil {
		t.Fatal(err)
	}
	// c2 remains EQUAL.
	got := a.AsyncRecipients()
	want := []ControllerID{c1, c2}
	if len(got) != len(want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipients = %v, want %v", got, want)
		}
	}

	// After c2 takes master, c1 is demoted and stops receiving async.
	if _, err := a.HandleRoleRequest(c2, RoleMaster, 5); err != nil {
		t.Fatal(err)
	}
	got = a.AsyncRecipients()
	want = []ControllerID{c2}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("recipients after switch = %v, want %v", got, want)
	}

	// With everything slave, asynchronous fan-out is empty.
	if _, err := a.HandleRoleRequest(c2, RoleSlave, 6); err != nil {
		t.Fatal(err)
	}
	if got := a.AsyncRecipients(); len(got) != 0 {
		t.Fatalf("all-slave recipients = %v, want empty", got)
	}
}

// Disconnect wipes role state and queued messages; a reconnect starts
// again at EQUAL with no generation memory, so the first new ROLE_REQUEST
// is accepted even with a low generation id.
func TestDisconnectReconnect(t *testing.T) {
	a := newConnected(t, c1, c2)
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 100); err != nil {
		t.Fatal(err)
	}
	if err := a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod, Xid: 1}); err != nil {
		t.Fatal(err)
	}
	a.Disconnect(c1)

	if a.Master() != "" {
		t.Fatalf("master after disconnect = %q, want empty", a.Master())
	}
	if _, ok := a.Role(c1); ok {
		t.Fatal("role state survived disconnect")
	}
	if a.Pending(c1) != 0 {
		t.Fatal("queue survived disconnect")
	}
	if a.Next(c1) != nil {
		t.Fatal("Next after disconnect must be nil")
	}

	// Reconnect: the old high generation id is forgotten.
	if err := a.Connect(c1); err != nil {
		t.Fatal(err)
	}
	if role, _ := a.Role(c1); role != RoleEqual {
		t.Fatalf("reconnect role = %s, want EQUAL", role)
	}
	if _, err := a.HandleRoleRequest(c1, RoleMaster, 1); err != nil {
		t.Fatalf("first request after reconnect rejected: %v", err)
	}
	if a.Master() != c1 {
		t.Fatalf("master = %q, want %q after reconnect", a.Master(), c1)
	}

	// Disconnecting an unknown id is a no-op rather than an error.
	a.Disconnect(ControllerID("ghost"))
}

// Concurrent role changes, enqueue/dequeue and fan-out must not race and
// must preserve the single-master invariant. Run with -race.
func TestConcurrentAccess(t *testing.T) {
	a := newConnected(t, c1, c2)
	var wg sync.WaitGroup
	for round := int64(0); round < 200; round++ {
		gen := round + 1
		wg.Add(4)
		go func(g int64) {
			defer wg.Done()
			_, _ = a.HandleRoleRequest(c1, RoleMaster, g)
		}(gen)
		go func(g int64) {
			defer wg.Done()
			_, _ = a.HandleRoleRequest(c2, RoleMaster, g)
		}(gen)
		go func() {
			defer wg.Done()
			_ = a.Enqueue(c1, &Envelope{MsgType: TypeFlowMod})
			_ = a.Enqueue(c2, &Envelope{MsgType: TypePacketOut})
			_ = a.Next(c1)
			_ = a.Next(c2)
		}()
		go func() {
			defer wg.Done()
			_ = a.AsyncRecipients()
			_ = a.Master()
		}()
	}
	wg.Wait()

	master := a.Master()
	if master != c1 && master != c2 && master != "" {
		t.Fatalf("unexpected master %q", master)
	}
	var masters []ControllerID
	for _, id := range []ControllerID{c1, c2} {
		if r, _ := a.Role(id); r == RoleMaster {
			masters = append(masters, id)
		}
	}
	if len(masters) > 1 {
		t.Fatalf("multiple masters: %v", masters)
	}
}
