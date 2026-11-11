package access

import (
	"errors"
	"testing"
)

type fakeDataPlane struct {
	installs   []uint64 // generations successfully passed to InstallKey
	installErr error
	acks       []uint64 // generations passed to SendAck
}

func (f *fakeDataPlane) install(_ string, gen uint64) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.installs = append(f.installs, gen)
	return nil
}

func (f *fakeDataPlane) ack(_ string, gen, _ uint64) {
	f.acks = append(f.acks, gen)
}

func newManager(t *testing.T, f *fakeDataPlane) *Manager {
	t.Helper()
	m, err := NewManager(Callbacks{InstallKey: f.install, SendAck: f.ack})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func reassoc(client string, gen uint64) Event {
	return Event{ClientID: client, Generation: gen, Kind: KindReassociate}
}

func confirm(client string, gen, replay uint64) Event {
	return Event{ClientID: client, Generation: gen, Kind: KindKeyConfirm, ReplayCounter: replay}
}

func TestHappyPath(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	if d := m.Handle(reassoc("sta1", 1)); d.Outcome != OutcomeHandshaking {
		t.Fatalf("reassoc: got %v", d)
	}
	if d := m.Handle(confirm("sta1", 1, 1)); d.Outcome != OutcomeInstalled {
		t.Fatalf("confirm: got %v", d)
	}
	st := m.Status("sta1")
	if st.State != StateEstablished || !st.HasChannel || st.EstablishedGen != 1 {
		t.Fatalf("status: %+v", st)
	}
	if len(f.installs) != 1 || len(f.acks) != 1 {
		t.Fatalf("installs=%v acks=%v", f.installs, f.acks)
	}
}

func TestRetransmittedConfirmReacksWithoutReinstall(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	m.Handle(confirm("sta1", 1, 1))

	// Same replay counter: classic retransmission.
	if d := m.Handle(confirm("sta1", 1, 1)); d.Outcome != OutcomeReacked {
		t.Fatalf("retransmitted confirm: got %v", d)
	}
	// Higher counter after install: still no reinstall.
	if d := m.Handle(confirm("sta1", 1, 2)); d.Outcome != OutcomeReacked {
		t.Fatalf("extra confirm: got %v", d)
	}
	if len(f.installs) != 1 {
		t.Fatalf("key reinstalled: installs=%v", f.installs)
	}
	if len(f.acks) != 3 {
		t.Fatalf("acks=%v", f.acks)
	}
}

func TestReassociationInstallsNewGenerationOnce(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	m.Handle(confirm("sta1", 1, 1))

	if d := m.Handle(reassoc("sta1", 2)); d.Outcome != OutcomeHandshaking {
		t.Fatalf("reassoc gen2: got %v", d)
	}
	// Old channel must still be the live one while gen2 is negotiated.
	if st := m.Status("sta1"); st.EstablishedGen != 1 || !st.HasChannel {
		t.Fatalf("old channel lost during rekey: %+v", st)
	}
	if d := m.Handle(confirm("sta1", 2, 1)); d.Outcome != OutcomeInstalled {
		t.Fatalf("confirm gen2: got %v", d)
	}
	if d := m.Handle(confirm("sta1", 2, 1)); d.Outcome != OutcomeReacked {
		t.Fatalf("retransmitted confirm gen2: got %v", d)
	}
	st := m.Status("sta1")
	if st.State != StateEstablished || st.EstablishedGen != 2 {
		t.Fatalf("status: %+v", st)
	}
	if len(f.installs) != 2 || f.installs[0] != 1 || f.installs[1] != 2 {
		t.Fatalf("installs=%v", f.installs)
	}
}

func TestStaleReassociationDropped(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 2))
	if d := m.Handle(reassoc("sta1", 2)); d.Outcome != OutcomeDropped {
		t.Fatalf("duplicate reassoc: got %v", d)
	}
	if d := m.Handle(reassoc("sta1", 1)); d.Outcome != OutcomeDropped {
		t.Fatalf("old reassoc: got %v", d)
	}
	if len(f.installs) != 0 || len(f.acks) != 0 {
		t.Fatalf("installs=%v acks=%v", f.installs, f.acks)
	}
}

func TestLateMessageFromOldGenerationDropped(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	m.Handle(confirm("sta1", 1, 1))
	m.Handle(reassoc("sta1", 2))
	m.Handle(confirm("sta1", 2, 1))

	// A confirm from generation 1 arrives late and must not touch the
	// data plane.
	if d := m.Handle(confirm("sta1", 1, 5)); d.Outcome != OutcomeDropped {
		t.Fatalf("late confirm: got %v", d)
	}
	if len(f.installs) != 2 || len(f.acks) != 2 {
		t.Fatalf("installs=%v acks=%v", f.installs, f.acks)
	}
	// Confirm for a generation never negotiated is dropped too.
	if d := m.Handle(confirm("sta1", 9, 1)); d.Outcome != OutcomeDropped {
		t.Fatalf("future confirm: got %v", d)
	}
	// Confirm with no handshake at all is dropped.
	if d := m.Handle(confirm("sta2", 1, 1)); d.Outcome != OutcomeDropped {
		t.Fatalf("confirm without handshake: got %v", d)
	}
}

func TestInstallFailureKeepsOldChannelAndAllowsRetry(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	m.Handle(confirm("sta1", 1, 1))

	// Rekey to generation 2; installation fails.
	m.Handle(reassoc("sta1", 2))
	f.installErr = errors.New("data plane busy")
	if d := m.Handle(confirm("sta1", 2, 1)); d.Outcome != OutcomeInstallFailed {
		t.Fatalf("failing confirm: got %v", d)
	}
	st := m.Status("sta1")
	if st.State != StateInstallFailed {
		t.Fatalf("state: %v", st.State)
	}
	if !st.HasChannel || st.EstablishedGen != 1 {
		t.Fatalf("old channel not explicit after failure: %+v", st)
	}
	if st.LastErr == nil {
		t.Fatal("LastErr not recorded")
	}
	if len(f.acks) != 1 {
		t.Fatalf("ack sent on failure: acks=%v", f.acks)
	}

	// The client retransmits the confirm; installation is retried and now
	// succeeds.
	f.installErr = nil
	if d := m.Handle(confirm("sta1", 2, 1)); d.Outcome != OutcomeInstalled {
		t.Fatalf("retry confirm: got %v", d)
	}
	st = m.Status("sta1")
	if st.State != StateEstablished || st.EstablishedGen != 2 || st.LastErr != nil {
		t.Fatalf("status after retry: %+v", st)
	}
	if len(f.installs) != 2 {
		t.Fatalf("installs=%v", f.installs)
	}
}

func TestInstallFailureWithoutPriorChannel(t *testing.T) {
	f := &fakeDataPlane{installErr: errors.New("no resources")}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	if d := m.Handle(confirm("sta1", 1, 1)); d.Outcome != OutcomeInstallFailed {
		t.Fatalf("failing confirm: got %v", d)
	}
	st := m.Status("sta1")
	if st.State != StateInstallFailed || st.HasChannel {
		t.Fatalf("status: %+v", st)
	}
}

func TestReassociationAfterInstallFailure(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("sta1", 1))
	m.Handle(confirm("sta1", 1, 1))
	m.Handle(reassoc("sta1", 2))
	f.installErr = errors.New("data plane busy")
	m.Handle(confirm("sta1", 2, 1))

	// A fresh reassociation opens generation 3; the failed generation is
	// abandoned and the old channel still backs the client.
	f.installErr = nil
	if d := m.Handle(reassoc("sta1", 3)); d.Outcome != OutcomeHandshaking {
		t.Fatalf("reassoc gen3: got %v", d)
	}
	if st := m.Status("sta1"); st.EstablishedGen != 1 {
		t.Fatalf("old channel: %+v", st)
	}
	if d := m.Handle(confirm("sta1", 3, 1)); d.Outcome != OutcomeInstalled {
		t.Fatalf("confirm gen3: got %v", d)
	}
	if st := m.Status("sta1"); st.EstablishedGen != 3 {
		t.Fatalf("status: %+v", st)
	}
}

func TestClientsAreIsolated(t *testing.T) {
	f := &fakeDataPlane{}
	m := newManager(t, f)

	m.Handle(reassoc("a", 1))
	m.Handle(reassoc("b", 1))
	m.Handle(confirm("a", 1, 1))

	if st := m.Status("b"); st.State != StateHandshaking || st.HasChannel {
		t.Fatalf("client b leaked state: %+v", st)
	}
	m.Forget("a")
	if st := m.Status("a"); st.State != StateIdle {
		t.Fatalf("forgotten client: %+v", st)
	}
}

func TestNewManagerRequiresCallbacks(t *testing.T) {
	if _, err := NewManager(Callbacks{}); err == nil {
		t.Fatal("expected error for missing callbacks")
	}
}
