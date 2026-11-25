package revco

import (
	"errors"
	"sort"
	"sync"
)

// BlockedChannel describes a channel whose advancement is required before more
// of the requested range can be proven revoked.
type BlockedChannel struct {
	Channel      ChanID
	CurrentWater Seq // watermark observed for the channel (0 if never seen)
	RequiredSeq  Seq // highest event sequence that must become committed
	Reason       string
}

// IsolateRequest asks the host-side coordinator to isolate a mapping range
// initially held by one process. Authorizations on the same object that do not
// descend from the named root are preserved.
type IsolateRequest struct {
	ID     string // idempotency key of the external revocation request
	Object ObjID
	Root   HoldKey // the process's original hold
	Range  Range
	Rights []Right
}

// Receipt is the bound result of an isolation request.
type Receipt struct {
	RequestID   string
	Complete    bool
	Object      ObjID
	Root        HoldKey
	Requested   Range
	Rights      []Right
	Revoked     []Range // byte fragments proven revoked in this run
	Unknown     []Range // byte fragments still unprovable
	Blocked     []BlockedChannel
	Watermarks  map[ChanID]Seq // vector of every participating channel
	RevokedKeys []HoldKey
	Resumable   bool // true when a host failure left the request in-flight
}

// Host performs the actual kernel-side revoke of one materialized hold.
// Implementations must be idempotent: revoking an already-dead hold is a
// success.
type Host interface {
	Revoke(key HoldKey) error
}

// Store persists coordinator journal state across interruptions.
type Store interface {
	Load() (*Journal, error)
	Save(*Journal) error
}

// Journal is the durable coordinator state.
type Journal struct {
	Requests map[string]*IsolateRequest
	Receipts map[string]*Receipt
	Revoked  map[string][]HoldKey // request id -> holds already revoked at host
}

func newJournal() *Journal {
	return &Journal{
		Requests: map[string]*IsolateRequest{},
		Receipts: map[string]*Receipt{},
		Revoked:  map[string][]HoldKey{},
	}
}

// Coordinator drives isolation over one shared Graph.
type Coordinator struct {
	graph *Graph
	host  Host
	store Store
	mu    sync.Mutex
}

// NewCoordinator builds a coordinator and replays any durable journal.
func NewCoordinator(g *Graph, host Host, store Store) (*Coordinator, error) {
	c := &Coordinator{graph: g, host: host, store: store}
	if store != nil {
		j, err := store.Load()
		if err != nil {
			return nil, err
		}
		if j == nil {
			if err := store.Save(newJournal()); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

func (c *Coordinator) journal() *Journal {
	if c.store == nil {
		return newJournal()
	}
	j, err := c.store.Load()
	if err != nil || j == nil {
		return newJournal()
	}
	return j
}

func (c *Coordinator) save(j *Journal) error {
	if c.store == nil {
		return nil
	}
	return c.store.Save(j)
}

// ErrRequestMismatch is returned when an idempotency key is reused with a
// different request body.
var ErrRequestMismatch = errors.New("revco: idempotent request reused with different parameters")

// Isolate executes (or resumes) one external revocation request. The same
// request ID always yields the same logical receipt; host revocations already
// journaled before an interruption are not re-attempted as new work.
func (c *Coordinator) Isolate(req IsolateRequest) (*Receipt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.ID == "" {
		return nil, errors.New("revco: isolate request requires an idempotency id")
	}
	j := c.journal()
	if prior, ok := j.Receipts[req.ID]; ok {
		if !sameRequest(j.Requests[req.ID], &req) {
			return nil, ErrRequestMismatch
		}
		return prior, nil
	}
	if saved, ok := j.Requests[req.ID]; ok && !sameRequest(saved, &req) {
		return nil, ErrRequestMismatch
	}
	j.Requests[req.ID] = &req
	if err := c.save(j); err != nil {
		return nil, err
	}

	snap := c.graph.Snapshot()
	already := map[HoldKey]bool{}
	for _, k := range j.Revoked[req.ID] {
		already[k] = true
	}

	children := map[HoldKey][]HoldKey{}
	var tainted map[HoldKey]bool
	if root, ok := snap.Holds[req.Root]; ok && root.Object == req.Object {
		tainted = descendantTaint(root.Key, snap.Holds, children)
	} else {
		tainted = map[HoldKey]bool{req.Root: true}
		growFrom(req.Root, children, tainted)
	}

	blocked := map[ChanID]*BlockedChannel{}
	participating := map[ChanID]Seq{}
	for ch, w := range snap.Watermarks {
		participating[ch] = w
	}
	var blockedRanges []Range

	// Committed events that could not be applied but lie inside the tainted
	// lineage bound the evidence we are allowed to trust.
	for _, ev := range snap.Nonapplied {
		if !c.eventInScope(ev, req, tainted, snap.Holds) {
			continue
		}
		c.markBlocked(blocked, participating, snap, ev.Channel, ev.Seq, "committed event awaiting unresolved source")
		blockedRanges = append(blockedRanges, c.eventRange(ev, req, snap))
		if ev.Type == EvReceive {
			c.markBlocked(blocked, participating, snap, ev.DestChan, 0, "in-flight message receive not yet observed")
		}
	}

	// Early events (observed but beyond their channel watermark) must not cross
	// the watermark: their effects stay unproven.
	for _, ev := range snap.Early {
		if !c.eventInScope(ev, req, tainted, snap.Holds) {
			continue
		}
		c.markBlocked(blocked, participating, snap, ev.Channel, ev.Seq, "event beyond channel watermark")
		blockedRanges = append(blockedRanges, c.eventRange(ev, req, snap))
	}

	// Live in-flight messages whose receive has never been observed are a
	// potential in-transit derivation channel; their bytes stay unprovable and
	// are collected before any hold is treated as fully evidenced.
	for k, h := range snap.Holds {
		if !tainted[k] || h.Object != req.Object || h.Msg == nil || h.Dead {
			continue
		}
		if intersect(h.Range, req.Range).Empty() {
			continue
		}
		ch := c.graph.sendDestChan(k)
		if ch == "" {
			ch = c.messageDestChan(k, snap)
		}
		c.markBlocked(blocked, participating, snap, ch, 0, "sent message not yet received")
		blockedRanges = append(blockedRanges, intersect(h.Range, req.Range))
	}

	var revokedKeys []HoldKey
	evidenced := map[Right][]Range{}
	keys := make([]HoldKey, 0, len(tainted))
	for k := range tainted {
		keys = append(keys, k)
	}
	sortHoldKeys(keys)

	for _, k := range keys {
		h, ok := snap.Holds[k]
		if !ok || h.Object != req.Object {
			continue
		}
		ov := intersect(h.Range, req.Range)
		if ov.Empty() {
			continue
		}
		for r := range h.Rights {
			if containsRight(req.Rights, r) {
				evidenced[r] = append(evidenced[r], ov)
			}
		}
		if h.Dead || already[k] {
			continue
		}
		if c.host != nil {
			if err := c.host.Revoke(k); err != nil {
				receipt := c.partialReceipt(req, revokedKeys, evidenced, blockedRanges, blocked, participating, true)
				if saveErr := c.save(j); saveErr != nil {
					return nil, saveErr
				}
				return receipt, err
			}
		}
		j.Revoked[req.ID] = append(j.Revoked[req.ID], k)
		if err := c.save(j); err != nil {
			return nil, err
		}
		revokedKeys = append(revokedKeys, k)
		already[k] = true
	}

	receipt := c.partialReceipt(req, revokedKeys, evidenced, blockedRanges, blocked, participating, false)
	j.Receipts[req.ID] = receipt
	if err := c.save(j); err != nil {
		return nil, err
	}
	return receipt, nil
}

// sortHoldKeys orders holds by generation and then HoldID so host revocation
// order is deterministic.
func sortHoldKeys(keys []HoldKey) {
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Gen != b.Gen {
			return a.Gen < b.Gen
		}
		return string(a.ID) < string(b.ID)
	})
}

// descendantTaint returns root together with every hold transitively derived
// from it across processes and message transfers.
func descendantTaint(root HoldKey, holds map[HoldKey]*Hold, children map[HoldKey][]HoldKey) map[HoldKey]bool {
	for k, h := range holds {
		for _, s := range h.Sources {
			children[s] = append(children[s], k)
		}
	}
	out := map[HoldKey]bool{root: true}
	growFrom(root, children, out)
	return out
}

func growFrom(root HoldKey, children map[HoldKey][]HoldKey, out map[HoldKey]bool) {
	queue := []HoldKey{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range children[cur] {
			if !out[child] {
				out[child] = true
				queue = append(queue, child)
			}
		}
	}
}

func (c *Coordinator) eventInScope(ev Event, req IsolateRequest, tainted map[HoldKey]bool, holds map[HoldKey]*Hold) bool {
	if ev.Object != "" && ev.Object != req.Object {
		return false
	}
	if ev.newKey() == req.Root || tainted[ev.srcKey()] {
		return true
	}
	if src, ok := holds[ev.srcKey()]; ok && tainted[src.Key] {
		return true
	}
	return false
}

func (c *Coordinator) messageDestChan(msg HoldKey, snap Snapshot) ChanID {
	for _, ev := range snap.Nonapplied {
		if ev.Type == EvReceive && ev.srcKey() == msg && ev.DestChan != "" {
			return ev.DestChan
		}
	}
	ch := c.graph.sendDestChan(msg)
	if ch != "" {
		return ch
	}
	return ChanID(string(msg.ID) + "-dest")
}

// eventRange returns the request-bounded bytes an unresolvable event might
// still carry. When the event names no explicit range the whole requested
// range must be treated as unprovable.
func (c *Coordinator) eventRange(ev Event, req IsolateRequest, snap Snapshot) Range {
	r := ev.Range
	if r.Empty() {
		if src, ok := snap.Holds[ev.srcKey()]; ok {
			r = src.Range
		} else {
			r = req.Range
		}
	}
	if ov := intersect(r, req.Range); !ov.Empty() {
		return ov
	}
	return req.Range
}

func (c *Coordinator) markBlocked(blocked map[ChanID]*BlockedChannel, participating map[ChanID]Seq, snap Snapshot, ch ChanID, seq Seq, reason string) {
	if ch == "" {
		return
	}
	participating[ch] = snap.Watermarks[ch]
	b := blocked[ch]
	if b == nil {
		b = &BlockedChannel{Channel: ch, CurrentWater: snap.Watermarks[ch], Reason: reason}
		blocked[ch] = b
	}
	if seq > b.RequiredSeq {
		b.RequiredSeq = seq
	}
}

func (c *Coordinator) partialReceipt(req IsolateRequest, revokedKeys []HoldKey, evidenced map[Right][]Range, blockedRanges []Range, blocked map[ChanID]*BlockedChannel, participating map[ChanID]Seq, resumable bool) *Receipt {
	r := &Receipt{
		RequestID:  req.ID,
		Object:     req.Object,
		Root:       req.Root,
		Requested:  req.Range,
		Rights:     append([]Right(nil), req.Rights...),
		Watermarks: map[ChanID]Seq{},
		Resumable:  resumable,
	}

	var covered []Range
	for _, right := range req.Rights {
		covered = append(covered, unionRanges(evidenced[right])...)
	}
	covered = unionRanges(covered)
	var proven []Range
	for _, part := range covered {
		proven = append(proven, subtractSet(part, blockedRanges)...)
	}
	r.Revoked = intersectSet(req.Range, unionRanges(proven))
	unknown := unionRanges(blockedRanges)
	unknown = append(unknown, subtractSet(req.Range, covered)...)
	r.Unknown = unionRanges(unknown)

	chans := make([]ChanID, 0, len(blocked))
	for ch := range blocked {
		chans = append(chans, ch)
	}
	sort.Slice(chans, func(i, j int) bool { return chans[i] < chans[j] })
	for _, ch := range chans {
		r.Blocked = append(r.Blocked, *blocked[ch])
	}
	pchans := make([]ChanID, 0, len(participating))
	for ch := range participating {
		pchans = append(pchans, ch)
	}
	sort.Slice(pchans, func(i, j int) bool { return pchans[i] < pchans[j] })
	for _, ch := range pchans {
		r.Watermarks[ch] = participating[ch]
	}
	r.RevokedKeys = append([]HoldKey(nil), revokedKeys...)
	r.Complete = len(r.Unknown) == 0 && len(r.Blocked) == 0 && !resumable
	return r
}

func containsRight(rights []Right, want Right) bool {
	for _, r := range rights {
		if r == want {
			return true
		}
	}
	return false
}

func sameRequest(a, b *IsolateRequest) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Object != b.Object || a.Root != b.Root || a.Range != b.Range {
		return false
	}
	if len(a.Rights) != len(b.Rights) {
		return false
	}
	ra := rightsSet(a.Rights)
	for _, r := range b.Rights {
		if !ra[r] {
			return false
		}
	}
	return true
}
