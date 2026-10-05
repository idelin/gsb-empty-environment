// Package leasesim is a deterministic, replayable simulator for epoch-based
// leases and fencing tokens. It models a cluster of voters that grant
// half-open lease intervals, candidate leaders that must assemble a quorum
// certificate, and a protected storage node that fences stale writers.
//
// The only clock is an integer tick advanced explicitly by the harness.
// There is no real network, goroutine, or wall-clock dependency: every
// message is an envelope in a pending queue that the harness delivers,
// reorders, duplicates, or drops. Every harness action is recorded as an Op
// so a whole run can be serialized and replayed bit-for-bit.
package leasesim

import (
	"encoding/json"
	"fmt"
)

// Quorum is the number of distinct voter grants required to form a
// certificate out of 5 voters.
const Quorum = 3

// VoterNames lists the five voters in deterministic order.
var VoterNames = []string{"v1", "v2", "v3", "v4", "v5"}

// StorageName is the node name of the protected storage.
const StorageName = "store"

// Grant is a voter's promise that Owner exclusively holds epoch Epoch over
// the half-open tick interval [Start, End).
type Grant struct {
	Voter string `json:"voter"`
	Epoch int    `json:"epoch"`
	Owner string `json:"owner"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Cert is a quorum certificate: Quorum grants for the same epoch and owner.
// Expiry is the minimum End of the constituent grants; the certificate
// authorizes storage access only while tick < Expiry.
type Cert struct {
	Epoch  int     `json:"epoch"`
	Owner  string  `json:"owner"`
	Expiry int     `json:"expiry"`
	Grants []Grant `json:"grants"`
}

type msgKind int

const (
	msgGrantReq msgKind = iota
	msgGrantResp
	msgWrite
	msgWriteResp
)

// envelope is a single in-flight message. One struct carries all payloads to
// keep the pending queue trivially inspectable.
type envelope struct {
	ID         int
	From, To   string
	Kind       msgKind
	DeliverAt  int
	CampaignID string
	Epoch      int
	Owner      string
	Duration   int
	Granted    bool
	Start, End int
	Reason     string
	Cert       *Cert
	ReqID      string
	Value      string
	OK         bool
}

// node holds volatile transport state shared by all participants. It is
// reset on restart; durable state lives in the *Disk structs.
type node struct {
	paused bool
	outbox []*envelope
}

// Op is one recorded harness action. A run is exactly reproducible by
// replaying its Op sequence into a fresh Sim.
type Op struct {
	Kind  string `json:"kind"` // tick, flush, campaign, write, pause, resume, restart, drop, dup, deliver
	Node  string `json:"node,omitempty"`
	N     int    `json:"n,omitempty"`
	Epoch int    `json:"epoch,omitempty"`
	Dur   int    `json:"dur,omitempty"`
	ReqID string `json:"reqID,omitempty"`
	Value string `json:"value,omitempty"`
	Index int    `json:"index,omitempty"`
}

// Sim is the whole simulated world.
type Sim struct {
	now     int
	voters  map[string]*Voter
	leaders map[string]*Leader
	storage *Storage
	pending []*envelope
	msgSeq  int
	campSeq int
	events  []string
	ops     []Op
}

// New builds a fresh world: 5 voters with empty disks, one empty storage.
func New() *Sim {
	s := &Sim{
		voters:  make(map[string]*Voter),
		leaders: make(map[string]*Leader),
		storage: &Storage{disk: &StorageDisk{ByID: map[string]string{}}},
	}
	for _, name := range VoterNames {
		s.voters[name] = &Voter{id: name, disk: &VoterDisk{}}
	}
	return s
}

// Now returns the current tick.
func (s *Sim) Now() int { return s.now }

// Events returns the full human-readable output log: every grant, denial,
// certificate, commit, and rejection with its reason.
func (s *Sim) Events() []string { return append([]string(nil), s.events...) }

// Committed returns the committed write sequence in commit order.
func (s *Sim) Committed() []string {
	return append([]string(nil), s.storage.disk.Writes...)
}

// CertOf returns the named leader's current certificate, or nil.
func (s *Sim) CertOf(name string) *Cert {
	if l, ok := s.leaders[name]; ok {
		return l.cert
	}
	return nil
}

// GrantCount reports how many distinct grants the leader collected in its
// current campaign.
func (s *Sim) GrantCount(name string) int {
	if l, ok := s.leaders[name]; ok {
		return len(l.grants)
	}
	return 0
}

// PendingCount reports how many messages sit in the network queue.
func (s *Sim) PendingCount() int { return len(s.pending) }

// Ops returns the recorded harness operations for replay.
func (s *Sim) Ops() []Op { return append([]Op(nil), s.ops...) }

func (s *Sim) logf(format string, args ...interface{}) {
	s.events = append(s.events, fmt.Sprintf("t=%d %s", s.now, fmt.Sprintf(format, args...)))
}

// ---------------------------------------------------------------------------
// Harness operations (each records an Op, then executes)
// ---------------------------------------------------------------------------

// Tick advances the only clock by n ticks.
func (s *Sim) Tick(n int) { s.record(Op{Kind: "tick", N: n}) }

// Flush delivers every due message to every non-paused node until the
// system is quiescent.
func (s *Sim) Flush() { s.record(Op{Kind: "flush"}) }

// Campaign makes a leader request a lease of dur ticks at the given epoch
// from all voters.
func (s *Sim) Campaign(leader string, epoch, dur int) {
	s.record(Op{Kind: "campaign", Node: leader, Epoch: epoch, Dur: dur})
}

// Write makes a leader send a write to storage under its current
// certificate. reqID is the idempotency key used for dedup.
func (s *Sim) Write(leader, reqID, value string) {
	s.record(Op{Kind: "write", Node: leader, ReqID: reqID, Value: value})
}

// Pause freezes a node: it stops sending (outgoing mail queues locally) and
// incoming messages wait in the network.
func (s *Sim) Pause(name string) { s.record(Op{Kind: "pause", Node: name}) }

// Resume unfreezes a node and releases its queued outbox.
func (s *Sim) Resume(name string) { s.record(Op{Kind: "resume", Node: name}) }

// Restart crashes and reboots a node: volatile state (campaigns, collected
// grants, certificates, outbox) is lost; the durable disk survives.
func (s *Sim) Restart(name string) { s.record(Op{Kind: "restart", Node: name}) }

// Drop deletes pending message i (models packet loss).
func (s *Sim) Drop(i int) { s.record(Op{Kind: "drop", Index: i}) }

// Dup appends a copy of pending message i (models duplication).
func (s *Sim) Dup(i int) { s.record(Op{Kind: "dup", Index: i}) }

// DeliverOne force-delivers pending message i regardless of its due tick
// (models reordering). It is a no-op if the target is paused.
func (s *Sim) DeliverOne(i int) { s.record(Op{Kind: "deliver", Index: i}) }

func (s *Sim) record(op Op) {
	s.ops = append(s.ops, op)
	s.apply(op)
}

func (s *Sim) apply(op Op) {
	switch op.Kind {
	case "tick":
		s.now += op.N
	case "flush":
		s.doFlush()
	case "campaign":
		s.doCampaign(op.Node, op.Epoch, op.Dur)
	case "write":
		s.doWrite(op.Node, op.ReqID, op.Value)
	case "pause":
		s.findNode(op.Node).paused = true
		s.logf("node %s paused", op.Node)
	case "resume":
		s.doResume(op.Node)
	case "restart":
		s.doRestart(op.Node)
	case "drop":
		e := s.pending[op.Index]
		s.pending = append(s.pending[:op.Index], s.pending[op.Index+1:]...)
		s.logf("network drops message #%d (%s -> %s)", e.ID, e.From, e.To)
	case "dup":
		dup := *s.pending[op.Index]
		s.msgSeq++
		dup.ID = s.msgSeq
		s.pending = append(s.pending, &dup)
		s.logf("network duplicates message (%s -> %s) as #%d", dup.From, dup.To, dup.ID)
	case "deliver":
		e := s.pending[op.Index]
		if s.isPaused(e.To) {
			return
		}
		s.pending = append(s.pending[:op.Index], s.pending[op.Index+1:]...)
		s.deliver(e)
	default:
		panic("leasesim: unknown op " + op.Kind)
	}
}

// ---------------------------------------------------------------------------
// Replay
// ---------------------------------------------------------------------------

// MarshalOps serializes an op log to JSON.
func MarshalOps(ops []Op) ([]byte, error) { return json.MarshalIndent(ops, "", "  ") }

// Replay re-executes an op log on a fresh world and returns the result.
func Replay(ops []Op) *Sim {
	s := New()
	for _, op := range ops {
		s.apply(op)
	}
	return s
}

// ReplayJSON decodes a JSON op log and replays it.
func ReplayJSON(data []byte) (*Sim, error) {
	var ops []Op
	if err := json.Unmarshal(data, &ops); err != nil {
		return nil, err
	}
	return Replay(ops), nil
}

// ---------------------------------------------------------------------------
// Internals
// ---------------------------------------------------------------------------

func (s *Sim) leader(name string) *Leader {
	l, ok := s.leaders[name]
	if !ok {
		l = &Leader{id: name}
		s.leaders[name] = l
	}
	return l
}

func (s *Sim) findNode(name string) *node {
	if v, ok := s.voters[name]; ok {
		return &v.node
	}
	if name == StorageName {
		return &s.storage.node
	}
	return &s.leader(name).node
}

func (s *Sim) isPaused(name string) bool { return s.findNode(name).paused }

// send routes a message through the sender's outbox if the sender is paused,
// otherwise into the network with one tick of latency.
func (s *Sim) send(from *node, e *envelope) {
	s.msgSeq++
	e.ID = s.msgSeq
	e.DeliverAt = s.now + 1
	if from != nil && from.paused {
		from.outbox = append(from.outbox, e)
		return
	}
	s.pending = append(s.pending, e)
}

func (s *Sim) doFlush() {
	for {
		delivered := false
		for i := 0; i < len(s.pending); i++ {
			e := s.pending[i]
			if e.DeliverAt > s.now || s.isPaused(e.To) {
				continue
			}
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			s.deliver(e)
			delivered = true
			break
		}
		if !delivered {
			return
		}
	}
}

func (s *Sim) deliver(e *envelope) {
	if v, ok := s.voters[e.To]; ok {
		v.onGrantReq(s, e)
		return
	}
	if e.To == StorageName {
		s.storage.onWrite(s, e)
		return
	}
	l := s.leader(e.To)
	switch e.Kind {
	case msgGrantResp:
		l.onGrantResp(s, e)
	case msgWriteResp:
		l.onWriteResp(s, e)
	}
}

func (s *Sim) doResume(name string) {
	n := s.findNode(name)
	n.paused = false
	for _, e := range n.outbox {
		e.DeliverAt = s.now + 1
		s.pending = append(s.pending, e)
	}
	n.outbox = nil
	s.logf("node %s resumed", name)
}

func (s *Sim) doRestart(name string) {
	switch {
	case s.voters[name] != nil:
		v := s.voters[name]
		*v = Voter{id: v.id, disk: v.disk} // disk survives: promises are kept
	case name == StorageName:
		s.storage = &Storage{disk: s.storage.disk}
	default:
		l := s.leader(name)
		*l = Leader{id: l.id} // leaders keep nothing durable
	}
	s.logf("node %s restarted (durable state preserved)", name)
}

func (s *Sim) doCampaign(name string, epoch, dur int) {
	l := s.leader(name)
	s.campSeq++
	l.campaignID = fmt.Sprintf("c%d", s.campSeq)
	l.epoch = epoch
	l.grants = nil
	s.logf("leader %s campaigns epoch=%d dur=%d (campaign %s)", name, epoch, dur, l.campaignID)
	for _, vn := range VoterNames {
		s.send(&l.node, &envelope{
			From: name, To: vn, Kind: msgGrantReq,
			CampaignID: l.campaignID, Epoch: epoch, Owner: name, Duration: dur,
		})
	}
}

func (s *Sim) doWrite(name, reqID, value string) {
	l := s.leader(name)
	if l.cert == nil {
		s.logf("leader %s write id=%s rejected locally: no certificate (quorum not reached)", name, reqID)
		return
	}
	s.logf("leader %s sends write id=%s value=%q cert(epoch=%d,expiry=%d)",
		name, reqID, value, l.cert.Epoch, l.cert.Expiry)
	s.send(&l.node, &envelope{From: name, To: StorageName, Kind: msgWrite, Cert: l.cert, ReqID: reqID, Value: value})
}
