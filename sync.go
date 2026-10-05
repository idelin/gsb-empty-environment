package gsb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Protocol messages. All sizes are measured as JSON-encoded bytes.

type DigestRequest struct {
	From    string
	Digests []Digest
}

type DigestResponse struct {
	Digests []Digest
}

// RangeRequest asks for the ordered records of one shard after a cursor.
// The cursor (After) is bound to Snapshot: the shard snapshot version the
// cursor was issued against.
type RangeRequest struct {
	From     string
	Shard    int
	After    string
	Snapshot uint64
	MaxBytes int
}

// RangeResponse carries one page of ordered records. Snapshot is the shard
// snapshot version this page belongs to. When Done, Final holds the
// full-shard digest so the receiver can verify the reassembled content.
// Restart tells the receiver its cursor's snapshot is stale and the shard
// pull must restart from the beginning rather than skip records.
type RangeResponse struct {
	Shard    int
	Snapshot uint64
	Records  []Record
	Done     bool
	Restart  bool
	Final    Digest
}

func sizeOf(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

var ErrOversized = errors.New("gsb: message exceeds byte limit")

// maxShardRetries bounds fault-driven retries of one shard within a round;
// anything left over is deferred to the next round.
const maxShardRetries = 4

// Engine runs the anti-entropy protocol for one node.
type Engine struct {
	ID           string
	Table        *Table
	Trust        *Trust
	MaxReqBytes  int
	MaxRespBytes int

	Stats Stats

	mu       sync.Mutex
	sessions map[string]map[int]*pullState // peer -> peer shard -> pull state
}

// pullState is the resumable state of one shard pull: the remote snapshot
// the cursor is bound to, the cursor itself, and the records buffered while
// waiting for the final digest verification.
type pullState struct {
	snapshot uint64
	after    string
	buf      []Record
	active   bool
}

func NewEngine(id string, table *Table, trust *Trust) *Engine {
	if trust == nil {
		trust = NewTrust()
	}
	return &Engine{
		ID:           id,
		Table:        table,
		Trust:        trust,
		MaxReqBytes:  4096,
		MaxRespBytes: 4096,
		sessions:     make(map[string]map[int]*pullState),
	}
}

// ServeDigest answers a digest exchange with this node's shard digests.
func (e *Engine) ServeDigest(req DigestRequest) DigestResponse {
	return DigestResponse{Digests: e.Table.Digests()}
}

// ServeRange serves one ordered page of a shard. The page is bounded by the
// request's byte budget but always contains at least one record when any
// remain, so the cursor always makes progress. A cursor bound to a stale
// snapshot is answered with Restart, never with a skipped range.
func (e *Engine) ServeRange(req RangeRequest) RangeResponse {
	resp := RangeResponse{Shard: req.Shard}
	if req.Shard < 0 || req.Shard >= e.Table.NumShards() {
		resp.Restart = true
		return resp
	}
	d := e.Table.Digest(req.Shard)
	resp.Snapshot = d.Version
	if req.Snapshot != d.Version {
		resp.Restart = true
		return resp
	}
	recs, _ := e.Table.Range(req.Shard, req.After)
	budget := req.MaxBytes
	if budget <= 0 || budget > e.MaxRespBytes {
		budget = e.MaxRespBytes
	}
	size := sizeOf(resp)
	n := 0
	for n < len(recs) {
		delta := sizeOf(recs[n]) + 1
		if n > 0 && size+delta > budget {
			break
		}
		size += delta
		n++
	}
	resp.Records = recs[:n]
	if n >= len(recs) {
		resp.Done = true
		resp.Final = d
	}
	return resp
}

// Round performs one anti-entropy round with a peer: exchange shard
// digests, then pull only the shards whose digests disagree.
func (e *Engine) Round(ctx context.Context, peer string, link *Link) error {
	rs := RoundStat{Peer: peer}
	defer func() { e.Stats.Add(rs) }()

	dreq := DigestRequest{From: e.ID, Digests: e.Table.Digests()}
	if n := sizeOf(dreq); n > e.MaxReqBytes {
		return fmt.Errorf("%w: digest request %d > %d bytes", ErrOversized, n, e.MaxReqBytes)
	}
	rs.ReqBytes += sizeOf(dreq)
	msg, err := link.Send(ctx, dreq)
	if err != nil {
		e.Trust.Penalize(peer)
		return err
	}
	dresp, ok := msg.(DigestResponse)
	if !ok {
		e.Trust.Penalize(peer)
		rs.Rejected++
		return fmt.Errorf("gsb: unexpected digest response %T", msg)
	}
	rs.RespBytes += sizeOf(dresp)
	if sizeOf(dresp) > e.MaxRespBytes {
		e.Trust.Penalize(peer)
		rs.Rejected++
		return fmt.Errorf("%w: digest response %d > %d bytes", ErrOversized, sizeOf(dresp), e.MaxRespBytes)
	}

	mine := e.Table.Digests()
	if len(dresp.Digests) == 0 {
		e.Trust.Reward(peer)
		return nil
	}
	byShard := make(map[int]Digest, len(dresp.Digests))
	for _, d := range dresp.Digests {
		byShard[d.Shard] = d
	}
	// A different shard count means a different sharding configuration:
	// shard boundaries are incomparable, so every remote shard is pulled.
	// Records are global; shards only index the transfer.
	configMismatch := len(dresp.Digests) != len(mine) ||
		dresp.Digests[0].NumShards != mine[0].NumShards
	var pull []Digest
	if configMismatch {
		pull = append(pull, dresp.Digests...)
	} else {
		for _, d := range dresp.Digests {
			if d.Shard < 0 || d.Shard >= len(mine) || !d.SameContent(mine[d.Shard]) {
				pull = append(pull, d)
			}
		}
	}

	for _, d := range pull {
		if err := e.pullShard(ctx, peer, link, d, &rs); err != nil {
			e.Trust.Penalize(peer)
			return err
		}
	}
	e.Trust.Reward(peer)
	return nil
}

// pullShard pulls one remote shard page by page. Received records are
// buffered and only applied after the final digest verifies the complete
// reassembled shard, so incomplete or forged transfers never reach the
// table. The merge itself is a lattice join, so even a verified-but-stale
// page cannot overwrite newer local records.
func (e *Engine) pullShard(ctx context.Context, peer string, link *Link, d Digest, rs *RoundStat) error {
	ps := e.session(peer, d.Shard)
	if !ps.active || ps.snapshot != d.Version {
		*ps = pullState{snapshot: d.Version, active: true}
	}
	retries := maxShardRetries
	for {
		req := RangeRequest{
			From:     e.ID,
			Shard:    d.Shard,
			After:    ps.after,
			Snapshot: ps.snapshot,
			MaxBytes: e.MaxRespBytes,
		}
		if n := sizeOf(req); n > e.MaxReqBytes {
			return fmt.Errorf("%w: range request %d > %d bytes", ErrOversized, n, e.MaxReqBytes)
		}
		rs.ReqBytes += sizeOf(req)
		msg, err := link.Send(ctx, req)
		if err != nil {
			return err // cursor persists in the session for resumption
		}
		resp, ok := msg.(RangeResponse)
		if !ok {
			rs.Rejected++
			return fmt.Errorf("gsb: unexpected range response %T", msg)
		}
		rs.RespBytes += sizeOf(resp)
		if sizeOf(resp) > e.MaxRespBytes {
			rs.Rejected++
			return fmt.Errorf("%w: range response %d > %d bytes", ErrOversized, sizeOf(resp), e.MaxRespBytes)
		}
		if resp.Restart || resp.Snapshot != ps.snapshot {
			// The remote snapshot moved under the cursor: restart the
			// shard from the new snapshot instead of skipping records.
			rs.Restarts++
			ps.snapshot = resp.Snapshot
			ps.after = ""
			ps.buf = ps.buf[:0]
			if retries--; retries <= 0 {
				return nil
			}
			continue
		}
		recs, ok := filterRange(resp.Records, ps.after, d.Shard, d.NumShards)
		if !ok || (len(recs) == 0 && !resp.Done) {
			// Wrong shard, out-of-window page, or no progress: reject
			// and retry; the cursor is left untouched.
			rs.Rejected++
			if retries--; retries <= 0 {
				return nil
			}
			continue
		}
		ps.buf = append(ps.buf, recs...)
		if len(recs) > 0 {
			ps.after = recs[len(recs)-1].NodeID
		}
		rs.CursorAdvance += len(recs)
		if !resp.Done {
			continue
		}
		merged := mergeBuffer(ps.buf)
		if !verifyShard(d.Shard, d.NumShards, resp.Final, merged) {
			// Incomplete or forged content: drop the buffer and
			// restart the shard from the current snapshot.
			rs.Rejected++
			rs.Restarts++
			ps.snapshot = resp.Snapshot
			ps.after = ""
			ps.buf = ps.buf[:0]
			if retries--; retries <= 0 {
				return nil
			}
			continue
		}
		rs.RecordsPulled += len(merged)
		e.Table.Apply(merged...)
		ps.active = false
		ps.buf = nil
		return nil
	}
}

// filterRange drops duplicates and overlaps (records at or before the
// cursor) and rejects pages containing records outside the requested
// shard. Anything missed here is still caught by the final digest check.
func filterRange(recs []Record, after string, shard, numShards int) ([]Record, bool) {
	out := make([]Record, 0, len(recs))
	prev := after
	for _, r := range recs {
		if r.NodeID <= prev {
			continue // duplicate or overlap: skip
		}
		if ShardOf(r.NodeID, numShards) != shard {
			return nil, false
		}
		out = append(out, r)
		prev = r.NodeID
	}
	return out, true
}

// mergeBuffer deduplicates a shard buffer by NodeID, keeping the dominant
// record, and returns it in canonical order.
func mergeBuffer(buf []Record) []Record {
	m := make(map[string]Record, len(buf))
	for _, r := range buf {
		cur, ok := m[r.NodeID]
		if !ok || r.Compare(cur) > 0 {
			m[r.NodeID] = r
		}
	}
	out := make([]Record, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// verifyShard checks that the reassembled shard content matches the digest
// the responder advertised for the snapshot.
func verifyShard(shard, numShards int, d Digest, recs []Record) bool {
	if d.Shard != shard || d.NumShards != numShards || d.Count != len(recs) {
		return false
	}
	return d.Hash == HashRecords(shard, numShards, recs)
}

func (e *Engine) session(peer string, shard int) *pullState {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.sessions[peer]
	if !ok {
		m = make(map[int]*pullState)
		e.sessions[peer] = m
	}
	ps, ok := m[shard]
	if !ok {
		ps = &pullState{}
		m[shard] = ps
	}
	return ps
}
