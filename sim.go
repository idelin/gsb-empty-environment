// Package leasim is a deterministic, replayable simulator for lease-based
// primary election protected by fencing tokens. It uses only the Go standard
// library, a virtual integer tick clock, and an in-memory message queue, so
// no real network or sleeping is involved.
package leasim

import "fmt"

// Tick is the only clock in the system: a virtual integer.
type Tick int64

// Quorum is the number of same-epoch, same-owner grants a candidate primary
// must collect before it may touch the storage.
const Quorum = 3

var voterIDs = []string{"v1", "v2", "v3", "v4", "v5"}

// Cert is the fencing-token certificate a primary presents to the storage.
// Expiry is the minimum deadline of the three grants that formed it.
type Cert struct {
	Epoch  int
	Owner  string
	Expiry Tick
}

// Grant is a lease granted by a single voter. The validity interval is
// half-open: [Start, End). A write at tick == End is already expired.
type Grant struct {
	Voter string
	Epoch int
	Owner string
	Start Tick
	End   Tick
}

// VoterDisk is the persistent state of a voter. It survives restarts:
// promises (max epoch, granted lease) are never lost.
type VoterDisk struct {
	MaxEpoch int
	Grant    *Grant
}

// Voter is a lease voter. Its volatile memory is empty; everything that
// matters lives on its disk.
type Voter struct {
	id   string
	disk *VoterDisk
}

// requestLease handles a lease request at the given tick.
func (v *Voter) requestLease(epoch int, owner string, start, end Tick, now Tick) (Grant, string) {
	d := v.disk
	if epoch < d.MaxEpoch {
		return Grant{}, fmt.Sprintf("voter %s rejects %s epoch %d: below persisted max epoch %d",
			v.id, owner, epoch, d.MaxEpoch)
	}
	if g := d.Grant; g != nil && g.Owner != owner && g.End > now && start < g.End && g.Start < end {
		return Grant{}, fmt.Sprintf("voter %s rejects %s epoch %d: overlapping valid lease held by %s [%d,%d)",
			v.id, owner, epoch, g.Owner, g.Start, g.End)
	}
	d.MaxEpoch = epoch
	g := Grant{Voter: v.id, Epoch: epoch, Owner: owner, Start: start, End: end}
	d.Grant = &g
	return g, ""
}

// Primary is a candidate primary. grants and cert are volatile: they are
// lost on restart. The outbox (queued writes) survives pause and restart,
// which is exactly how a stale primary ends up resubmitting old writes.
type Primary struct {
	id     string
	epoch  int
	grants map[string]Grant
	cert   *Cert
	outbox []Message
}

// WriteRequest is a storage write carrying the primary's certificate.
type WriteRequest struct {
	ReqID string
	Value string
	Cert  Cert
}

// WriteRecord is one committed write.
type WriteRecord struct {
	ReqID string
	Value string
	Epoch int
	At    Tick
}

// Storage is the protected store. It fences by the maximum epoch it has
// seen, rejects expired certificates, and dedups retries by request ID.
type Storage struct {
	maxEpoch  int
	committed []WriteRecord
	byID      map[string]string
}

// write validates the call tick, the certificate and the fencing epoch,
// then applies idempotently by request ID. Returns ("", true) on success
// (including duplicate retries) or (reason, false) on rejection.
func (st *Storage) write(req WriteRequest, now Tick) (string, bool) {
	if req.Cert.Epoch < st.maxEpoch {
		return fmt.Sprintf("stale epoch %d: storage fenced at epoch %d", req.Cert.Epoch, st.maxEpoch), false
	}
	if now >= req.Cert.Expiry {
		return fmt.Sprintf("certificate expired at tick %d (now %d)", req.Cert.Expiry, now), false
	}
	if req.Cert.Epoch > st.maxEpoch {
		st.maxEpoch = req.Cert.Epoch
	}
	if prev, ok := st.byID[req.ReqID]; ok {
		if prev != req.Value {
			return fmt.Sprintf("conflicting retry: request %s already committed with a different value", req.ReqID), false
		}
		return "", true // duplicate retry: idempotent success, applied once
	}
	st.byID[req.ReqID] = req.Value
	st.committed = append(st.committed, WriteRecord{ReqID: req.ReqID, Value: req.Value, Epoch: req.Cert.Epoch, At: now})
	return "", true
}

// LeaseRequest asks a voter for the half-open interval [Start, End).
type LeaseRequest struct {
	Epoch int
	Owner string
	Start Tick
	End   Tick
}

// Message is one in-flight network message. Exactly one field is non-nil.
type Message struct {
	From  string
	To    string
	Lease *LeaseRequest
	Grant *Grant
	Write *WriteRequest
}

func (m Message) String() string {
	switch {
	case m.Lease != nil:
		return fmt.Sprintf("lease-req %s->%s epoch=%d [%d,%d)", m.From, m.To, m.Lease.Epoch, m.Lease.Start, m.Lease.End)
	case m.Grant != nil:
		return fmt.Sprintf("grant %s->%s epoch=%d [%d,%d)", m.From, m.To, m.Grant.Epoch, m.Grant.Start, m.Grant.End)
	case m.Write != nil:
		return fmt.Sprintf("write %s->%s %s=%s epoch=%d", m.From, m.To, m.Write.ReqID, m.Write.Value, m.Write.Cert.Epoch)
	}
	return "empty"
}

// Command is one deterministic step of a scenario script. The sequence of
// commands is the replayable log.
type Command struct {
	Op    string // advance, campaign, renew, write, deliver, deliverOne, drop, dup, reverse, pause, resume, restartVoter, restartPrimary
	Node  string
	Epoch int
	ReqID string
	Value string
	To    Tick
	Index int
}

// Sim is the whole simulated world.
type Sim struct {
	now        Tick
	leaseLen   Tick
	voters     map[string]*Voter
	disks      map[string]*VoterDisk
	primaries  map[string]*Primary
	storage    *Storage
	paused     map[string]bool
	queue      []Message
	records    []string
	rejections []string
	cmds       []Command
}

// New builds a fresh world: 5 voters with empty disks, one storage.
func New(leaseLen Tick) *Sim {
	s := &Sim{
		leaseLen:  leaseLen,
		voters:    map[string]*Voter{},
		disks:     map[string]*VoterDisk{},
		primaries: map[string]*Primary{},
		storage:   &Storage{byID: map[string]string{}},
		paused:    map[string]bool{},
	}
	for _, id := range voterIDs {
		d := &VoterDisk{}
		s.disks[id] = d
		s.voters[id] = &Voter{id: id, disk: d}
	}
	return s
}

func (s *Sim) record(format string, args ...interface{}) {
	s.records = append(s.records, fmt.Sprintf("tick %3d: ", s.now)+fmt.Sprintf(format, args...))
}

func (s *Sim) reject(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	s.rejections = append(s.rejections, fmt.Sprintf("tick %d: %s", s.now, msg))
	s.record("REJECT %s", msg)
}

func (s *Sim) primary(id string) *Primary {
	p, ok := s.primaries[id]
	if !ok {
		p = &Primary{id: id, grants: map[string]Grant{}}
		s.primaries[id] = p
	}
	return p
}

// Do appends a command to the log and executes it.
func (s *Sim) Do(c Command) {
	s.cmds = append(s.cmds, c)
	s.exec(c)
}

// Run executes a whole script.
func (s *Sim) Run(cmds []Command) {
	for _, c := range cmds {
		s.Do(c)
	}
}

// Replay rebuilds a fresh world from a recorded log. Determinism is
// verified by comparing Records/Committed of the original and the replay.
func Replay(leaseLen Tick, cmds []Command) *Sim {
	s := New(leaseLen)
	s.Run(cmds)
	return s
}

// Log returns the recorded command log.
func (s *Sim) Log() []Command { return s.cmds }

// Records returns the full human-readable event log.
func (s *Sim) Records() []string { return s.records }

// Rejections returns every rejection reason, in order.
func (s *Sim) Rejections() []string { return s.rejections }

// Committed returns the committed write sequence.
func (s *Sim) Committed() []WriteRecord { return s.storage.committed }

// CertOf returns the current certificate of a primary, or nil.
func (s *Sim) CertOf(id string) *Cert {
	if p, ok := s.primaries[id]; ok {
		return p.cert
	}
	return nil
}

// QueueLen reports the number of in-flight messages.
func (s *Sim) QueueLen() int { return len(s.queue) }

func (s *Sim) exec(c Command) {
	switch c.Op {
	case "advance":
		if c.To < s.now {
			panic("clock cannot go backwards")
		}
		s.now = c.To
		s.record("clock advanced to %d", c.To)
	case "campaign":
		s.campaign(c.Node, c.Epoch)
	case "renew":
		s.campaign(c.Node, s.primary(c.Node).epoch)
	case "write":
		s.submitWrite(c.Node, c.ReqID, c.Value)
	case "deliver":
		s.deliverAll()
	case "deliverOne":
		m := s.queue[c.Index]
		s.queue = append(s.queue[:c.Index], s.queue[c.Index+1:]...)
		s.dispatch(m)
	case "drop":
		s.record("drop message: %s", s.queue[c.Index])
		s.queue = append(s.queue[:c.Index], s.queue[c.Index+1:]...)
	case "dup":
		s.queue = append(s.queue, s.queue[c.Index])
		s.record("duplicate message: %s", s.queue[c.Index])
	case "reverse":
		for i, j := 0, len(s.queue)-1; i < j; i, j = i+1, j-1 {
			s.queue[i], s.queue[j] = s.queue[j], s.queue[i]
		}
		s.record("reorder: reversed %d in-flight messages", len(s.queue))
	case "pause":
		s.paused[c.Node] = true
		s.record("node %s paused", c.Node)
	case "resume":
		s.paused[c.Node] = false
		s.record("node %s resumed", c.Node)
		if p, ok := s.primaries[c.Node]; ok && len(p.outbox) > 0 {
			s.record("primary %s flushes %d queued message(s) from outbox", c.Node, len(p.outbox))
			s.queue = append(s.queue, p.outbox...)
			p.outbox = nil
		}
	case "restartVoter":
		s.voters[c.Node] = &Voter{id: c.Node, disk: s.disks[c.Node]}
		s.record("voter %s restarted; promises retained on disk (max epoch %d)", c.Node, s.disks[c.Node].MaxEpoch)
	case "restartPrimary":
		p := s.primary(c.Node)
		p.grants = map[string]Grant{}
		p.cert = nil
		s.record("primary %s restarted; volatile certificate lost", c.Node)
	default:
		panic("unknown op: " + c.Op)
	}
}

func (s *Sim) campaign(id string, epoch int) {
	p := s.primary(id)
	p.epoch = epoch
	p.grants = map[string]Grant{}
	if p.cert != nil && p.cert.Epoch != epoch {
		p.cert = nil
	}
	s.record("primary %s campaigns for epoch %d, wants lease [%d,%d)", id, epoch, s.now, s.now+s.leaseLen)
	for _, vid := range voterIDs {
		s.sendOrQueue(p, Message{From: id, To: vid,
			Lease: &LeaseRequest{Epoch: epoch, Owner: id, Start: s.now, End: s.now + s.leaseLen}})
	}
}

func (s *Sim) submitWrite(id, reqID, value string) {
	p := s.primary(id)
	if p.cert == nil {
		s.reject("primary %s write %s=%s: no certificate (quorum of %d not reached)", id, reqID, value, Quorum)
		return
	}
	s.sendOrQueue(p, Message{From: id, To: "storage",
		Write: &WriteRequest{ReqID: reqID, Value: value, Cert: *p.cert}})
}

func (s *Sim) sendOrQueue(p *Primary, m Message) {
	if s.paused[p.id] {
		p.outbox = append(p.outbox, m)
		return
	}
	s.queue = append(s.queue, m)
}

// deliverAll drains the queue, including messages generated en route.
// Messages addressed to paused nodes stay in the queue.
func (s *Sim) deliverAll() {
	for {
		pending := s.queue
		s.queue = nil
		progress := false
		for _, m := range pending {
			if s.paused[m.To] {
				s.queue = append(s.queue, m)
				continue
			}
			progress = true
			s.dispatch(m)
		}
		if !progress {
			return
		}
	}
}

func (s *Sim) dispatch(m Message) {
	switch {
	case m.Lease != nil:
		v := s.voters[m.To]
		g, reason := v.requestLease(m.Lease.Epoch, m.Lease.Owner, m.Lease.Start, m.Lease.End, s.now)
		if reason != "" {
			s.reject("%s", reason)
			return
		}
		s.record("voter %s grants epoch %d to %s [%d,%d)", v.id, g.Epoch, g.Owner, g.Start, g.End)
		s.queue = append(s.queue, Message{From: v.id, To: m.From, Grant: &g})
	case m.Grant != nil:
		p, ok := s.primaries[m.To]
		if !ok {
			return
		}
		g := *m.Grant
		if g.Epoch != p.epoch || g.Owner != p.id || g.End <= s.now {
			s.record("primary %s ignores stale grant from %s (epoch %d, end %d)", p.id, g.Voter, g.Epoch, g.End)
			return
		}
		p.grants[g.Voter] = g
		if len(p.grants) == Quorum {
			expiry := g.End
			for _, gr := range p.grants {
				if gr.End < expiry {
					expiry = gr.End
				}
			}
			p.cert = &Cert{Epoch: p.epoch, Owner: p.id, Expiry: expiry}
			s.record("primary %s forms certificate epoch %d, expiry %d (min of %d grants)",
				p.id, p.epoch, expiry, Quorum)
		}
	case m.Write != nil:
		reason, ok := s.storage.write(*m.Write, s.now)
		if !ok {
			s.reject("write %s=%s from %s: %s", m.Write.ReqID, m.Write.Value, m.From, reason)
			return
		}
		s.record("storage accepts write %s=%s (epoch %d)", m.Write.ReqID, m.Write.Value, m.Write.Cert.Epoch)
	}
}
