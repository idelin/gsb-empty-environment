package lease

import (
	"bytes"
	"errors"
	"sync"
)

var (
	// ErrNoConflict is returned when resolving a conflict that does not
	// exist or choosing a source that is not part of it.
	ErrNoConflict = errors.New("lease: no such conflict or source")
	// ErrConflictDelivered is returned when the chosen conflict source
	// contradicts a payload the host inbox has already definitely
	// received; the business must reconcile out of band.
	ErrConflictDelivered = errors.New("lease: conflicting payload already delivered to host inbox")
)

// Disposition classifies the result of Receive.
type Disposition int

const (
	// Accepted means the datagram was staged and handed to the commit
	// protocol; see Outcome.State for the resulting commit state.
	Accepted Disposition = iota
	// Duplicate means the same logical sequence with the same payload
	// was already accepted; it is not delivered again.
	Duplicate
	// Frozen means the request is frozen by a sequence conflict and the
	// datagram was held back.
	Frozen
	// Rejected means the datagram was refused (no lease, expired
	// overlap, ambiguous or out-of-span sequence).
	Rejected
)

func (d Disposition) String() string {
	switch d {
	case Accepted:
		return "accepted"
	case Duplicate:
		return "duplicate"
	case Frozen:
		return "frozen"
	case Rejected:
		return "rejected"
	}
	return "invalid"
}

// Source identifies one path's copy of a contested datagram.
type Source struct {
	Generation Generation
	Conn       ConnectionID
	Payload    []byte
}

// Conflict records both sources of a sequence that arrived with
// different payloads on the two paths. Both copies are retained; the
// winner is never chosen by comparing arrival times.
type Conflict struct {
	Tenant        TenantID
	Auth          AuthID
	Seq           uint64
	First, Second Source
}

// NetAck is a transport-level acknowledgement, scoped to the connection
// and generation it arrived on. It is separate from business receipts
// and only releases staging of its own generation.
type NetAck struct {
	Conn   ConnectionID
	Stream StreamID
	Seq    uint64
}

// Outcome reports what Receive did with one datagram.
type Outcome struct {
	Disposition Disposition
	Lease       *Lease
	Seq         uint64
	// State is the commit state after an Accepted datagram.
	State CommitState
	// Ack is the transport ack to emit for accepted or duplicate
	// datagrams; nil for frozen or rejected ones.
	Ack *NetAck
	// Conflict is set when the datagram froze the request.
	Conflict *Conflict
}

type seenEntry struct {
	payload []byte
	src     Source
}

type requestState struct {
	frozen    bool
	conflicts []*Conflict
	seen      map[uint64]seenEntry
	// staged holds received payloads per generation awaiting the
	// network ack of that generation. Acks on the old path can never
	// release the new generation's entries.
	staged map[Generation]map[uint64][]byte
}

func newRequestState() *requestState {
	return &requestState{
		seen:   make(map[uint64]seenEntry),
		staged: make(map[Generation]map[uint64][]byte),
	}
}

// Dispatcher composes lease management, datagram dedup/freeze and the
// commit protocol into the receive path of the Go HTTP/3 dispatcher.
type Dispatcher struct {
	mgr *Manager
	rec *Receiver

	mu   sync.Mutex
	reqs map[reqKey]*requestState
}

// NewDispatcher builds a Dispatcher over durable store and host inbox.
func NewDispatcher(store Store, inbox Inbox, opts Options) *Dispatcher {
	return &Dispatcher{
		mgr:  NewManager(opts),
		rec:  NewReceiver(store, inbox),
		reqs: make(map[reqKey]*requestState),
	}
}

// Manager exposes the lease lifecycle.
func (d *Dispatcher) Manager() *Manager { return d.mgr }

// Receiver exposes the commit protocol.
func (d *Dispatcher) Receiver() *Receiver { return d.rec }

// Issue establishes a request; see Manager.Issue.
func (d *Dispatcher) Issue(tenant TenantID, conn ConnectionID, stream StreamID, auth AuthID, anchor uint64, maxSpan uint32) (*Lease, error) {
	l, err := d.mgr.Issue(tenant, conn, stream, auth, anchor, maxSpan)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.reqs[reqKey{tenant, auth}] = newRequestState()
	d.mu.Unlock()
	return l, nil
}

// Migrate moves a request to a new route; see Manager.Migrate. Dedup and
// conflict state carry over because they key on the business identity,
// not on the connection.
func (d *Dispatcher) Migrate(tenant TenantID, auth AuthID, newConn ConnectionID, newStream StreamID, anchor uint64, maxSpan uint32) (*Lease, error) {
	return d.mgr.Migrate(tenant, auth, newConn, newStream, anchor, maxSpan)
}

// Receive ingests one datagram. It resolves the lease (including
// superseded leases inside the overlap window), expands the 24-bit wire
// sequence, deduplicates by business identity, freezes the request on
// conflicting payloads, and hands new datagrams to the commit protocol.
func (d *Dispatcher) Receive(conn ConnectionID, stream StreamID, wireSeq uint32, payload []byte) (Outcome, error) {
	l, err := d.mgr.Route(conn, stream)
	if err != nil {
		return Outcome{Disposition: Rejected}, err
	}
	seq, err := Expand(wireSeq, l.Anchor, l.MaxSpan)
	if err != nil {
		return Outcome{Disposition: Rejected, Lease: l}, err
	}
	ack := &NetAck{Conn: conn, Stream: stream, Seq: seq}

	d.mu.Lock()
	rq := reqKey{l.Tenant, l.Auth}
	req := d.reqs[rq]
	if req == nil {
		req = newRequestState()
		d.reqs[rq] = req
	}
	if req.frozen {
		d.mu.Unlock()
		return Outcome{Disposition: Frozen, Lease: l, Seq: seq}, nil
	}
	if prev, ok := req.seen[seq]; ok {
		if bytes.Equal(prev.payload, payload) {
			d.mu.Unlock()
			return Outcome{Disposition: Duplicate, Lease: l, Seq: seq, Ack: ack}, nil
		}
		c := &Conflict{
			Tenant: l.Tenant,
			Auth:   l.Auth,
			Seq:    seq,
			First:  prev.src,
			Second: Source{Generation: l.Generation, Conn: conn, Payload: append([]byte(nil), payload...)},
		}
		req.conflicts = append(req.conflicts, c)
		req.frozen = true
		d.mu.Unlock()
		return Outcome{Disposition: Frozen, Lease: l, Seq: seq, Conflict: c}, nil
	}
	src := Source{Generation: l.Generation, Conn: conn, Payload: append([]byte(nil), payload...)}
	req.seen[seq] = seenEntry{payload: src.Payload, src: src}
	if req.staged[l.Generation] == nil {
		req.staged[l.Generation] = make(map[uint64][]byte)
	}
	req.staged[l.Generation][seq] = src.Payload
	d.mu.Unlock()

	rec := DeliveryRecord{Key: RecordKey{Tenant: l.Tenant, Auth: l.Auth, Seq: seq}, Payload: payload}
	deliverErr := d.rec.Deliver(rec)
	st, stateErr := d.rec.State(rec.Key)
	if stateErr != nil {
		// The record never reached the store: staging itself failed.
		return Outcome{Disposition: Rejected, Lease: l, Seq: seq}, deliverErr
	}
	return Outcome{Disposition: Accepted, Lease: l, Seq: seq, State: st, Ack: ack}, nil
}

// OnNetAck applies a transport-level acknowledgement. The ack resolves
// to the lease of the route it arrived on and releases only that
// generation's staged copy, so an ack on the old path cannot release the
// new generation's staging area. It reports whether anything was
// released.
func (d *Dispatcher) OnNetAck(conn ConnectionID, stream StreamID, wireSeq uint32) bool {
	l, err := d.mgr.Route(conn, stream)
	if err != nil {
		return false
	}
	seq, err := Expand(wireSeq, l.Anchor, l.MaxSpan)
	if err != nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	req := d.reqs[reqKey{l.Tenant, l.Auth}]
	if req == nil {
		return false
	}
	gen := req.staged[l.Generation]
	if _, ok := gen[seq]; !ok {
		return false
	}
	delete(gen, seq)
	return true
}

// Staged lists the logical sequences still staged for one generation of
// a request, i.e. not yet released by that generation's network acks.
func (d *Dispatcher) Staged(tenant TenantID, auth AuthID, gen Generation) []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	req := d.reqs[reqKey{tenant, auth}]
	if req == nil {
		return nil
	}
	out := make([]uint64, 0, len(req.staged[gen]))
	for seq := range req.staged[gen] {
		out = append(out, seq)
	}
	return out
}

// Conflicts returns the unresolved conflicts of a request.
func (d *Dispatcher) Conflicts(tenant TenantID, auth AuthID) []*Conflict {
	d.mu.Lock()
	defer d.mu.Unlock()
	req := d.reqs[reqKey{tenant, auth}]
	if req == nil {
		return nil
	}
	return append([]*Conflict(nil), req.conflicts...)
}

// Frozen reports whether a request is frozen by an unresolved conflict.
func (d *Dispatcher) Frozen(tenant TenantID, auth AuthID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	req := d.reqs[reqKey{tenant, auth}]
	return req != nil && req.frozen
}

// ResolveConflict lets the host explicitly pick the winning source of a
// conflict by generation. Arrival order is never consulted. If the host
// inbox already definitely received a different payload for the
// sequence, ResolveConflict refuses with ErrConflictDelivered. When the
// choice is consistent, the chosen payload is delivered through the
// commit protocol (unless already delivered) and the request unfreezes
// once its last conflict is resolved.
func (d *Dispatcher) ResolveConflict(tenant TenantID, auth AuthID, seq uint64, choose Generation) error {
	d.mu.Lock()
	req := d.reqs[reqKey{tenant, auth}]
	if req == nil {
		d.mu.Unlock()
		return ErrNoConflict
	}
	idx := -1
	var c *Conflict
	for i, x := range req.conflicts {
		if x.Seq == seq {
			idx, c = i, x
			break
		}
	}
	if c == nil {
		d.mu.Unlock()
		return ErrNoConflict
	}
	var chosen Source
	switch choose {
	case c.First.Generation:
		chosen = c.First
	case c.Second.Generation:
		chosen = c.Second
	default:
		d.mu.Unlock()
		return ErrNoConflict
	}
	seenPayload := req.seen[seq].payload
	d.mu.Unlock()

	key := RecordKey{Tenant: tenant, Auth: auth, Seq: seq}
	// The host inbox is the source of truth: if it definitely holds a
	// different payload, the choice contradicts a completed delivery.
	if rec, found, err := d.rec.inbox.Lookup(key); err == nil && found && !bytes.Equal(rec.Payload, chosen.Payload) {
		return ErrConflictDelivered
	}
	st, err := d.rec.State(key)
	if err != nil && !errors.Is(err, ErrRecordNotFound) {
		return err
	}
	if st == StateCommitted {
		// Already delivered locally and the inbox could not be checked:
		// only a choice consistent with the delivered payload is valid.
		if !bytes.Equal(seenPayload, chosen.Payload) {
			return ErrConflictDelivered
		}
	} else {
		if err := d.rec.Deliver(DeliveryRecord{Key: key, Payload: chosen.Payload}); err != nil {
			return err
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	req = d.reqs[reqKey{tenant, auth}]
	if req == nil {
		return nil
	}
	req.seen[seq] = seenEntry{payload: chosen.Payload, src: chosen}
	req.conflicts = append(req.conflicts[:idx], req.conflicts[idx+1:]...)
	if len(req.conflicts) == 0 {
		req.frozen = false
	}
	return nil
}
