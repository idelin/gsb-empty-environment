package syncx

import (
	"sort"
	"sync"

	"antientropy/member"
)

const (
	// StatusOK means the pull response carries records from the requested
	// snapshot.
	StatusOK = iota
	// StatusRestart tells the requester its snapshot cursor is invalid
	// (snapshot evicted or shard config changed). The requester must restart
	// the round from digest exchange — records are never silently skipped.
	StatusRestart
)

type DigestRequest struct {
	StartShard int
	MaxBytes   int
}

type DigestResponse struct {
	SnapshotVersion uint64
	NumShards       int
	Digests         []Digest
	NextShard       int // -1 when all digests have been delivered
	Bytes           int
}

type PullRequest struct {
	Shard           int
	SnapshotVersion uint64
	Cursor          string // exclusive lower bound; "" starts the shard
	MaxBytes        int
}

type PullResponse struct {
	Status          int
	SnapshotVersion uint64
	Records         []member.Record
	Checksum        uint64
	NextCursor      string
	Done            bool
	Bytes           int
}

// snapshot is an immutable, versioned view of the table partitioned into
// sorted shards. Cursors are only meaningful against one snapshot version.
type snapshot struct {
	version  uint64
	tableRev uint64
	shards   [][]member.Record
	digests  []Digest
}

// Server serves digests and pull pages from the local table.
type Server struct {
	Table *member.Table

	mu        sync.Mutex
	numShards int
	keep      int // snapshots retained for in-flight pulls
	nextVer   uint64
	latest    *snapshot
	snaps     map[uint64]*snapshot
	order     []uint64
}

func NewServer(numShards int) *Server {
	if numShards < 1 {
		numShards = 1
	}
	return &Server{
		Table:     member.NewTable(),
		numShards: numShards,
		keep:      2,
		snaps:     make(map[uint64]*snapshot),
	}
}

func (s *Server) NumShards() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.numShards
}

// SetNumShards applies a sharding-config change. All outstanding snapshots
// are invalidated: in-flight pulls get StatusRestart and resync against the
// new boundaries instead of skipping records.
func (s *Server) SetNumShards(n int) {
	if n < 1 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == s.numShards {
		return
	}
	s.numShards = n
	s.latest = nil
	s.snaps = make(map[uint64]*snapshot)
	s.order = nil
}

func buildSnapshot(version, tableRev uint64, recs []member.Record, numShards int) *snapshot {
	snap := &snapshot{version: version, tableRev: tableRev, shards: make([][]member.Record, numShards)}
	for _, r := range recs {
		sh := ShardOf(r.NodeID, numShards)
		snap.shards[sh] = append(snap.shards[sh], r)
	}
	snap.digests = make([]Digest, numShards)
	for sh := 0; sh < numShards; sh++ {
		sort.Slice(snap.shards[sh], func(i, j int) bool {
			return snap.shards[sh][i].NodeID < snap.shards[sh][j].NodeID
		})
		snap.digests[sh] = ComputeDigest(snap.shards[sh], sh, numShards)
	}
	return snap
}

// currentSnapshot returns a stable snapshot for the current table revision,
// creating and retaining a new version when the table changed.
func (s *Server) currentSnapshot() *snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	rev := s.Table.Revision()
	if s.latest != nil && s.latest.tableRev == rev {
		return s.latest
	}
	s.nextVer++
	snap := buildSnapshot(s.nextVer, rev, s.Table.Snapshot(), s.numShards)
	s.snaps[snap.version] = snap
	s.order = append(s.order, snap.version)
	for len(s.order) > s.keep {
		delete(s.snaps, s.order[0])
		s.order = s.order[1:]
	}
	s.latest = snap
	return snap
}

// DigestsFor computes fresh digests of the local table under the given
// sharding. Used to compare against a peer whose NumShards may differ
// (e.g. during a rolling shard-config change).
func (s *Server) DigestsFor(numShards int) []Digest {
	if numShards < 1 {
		numShards = 1
	}
	return buildSnapshot(0, s.Table.Revision(), s.Table.Snapshot(), numShards).digests
}

// HandleDigest serves digests paginated by the request byte limit. Every
// non-final page makes progress (at least one digest).
func (s *Server) HandleDigest(req DigestRequest) DigestResponse {
	snap := s.currentSnapshot()
	resp := DigestResponse{
		SnapshotVersion: snap.version,
		NumShards:       s.NumShards(),
		NextShard:       -1,
	}
	budget := req.MaxBytes - DigestResponseHeader
	for i := req.StartShard; i < len(snap.digests); i++ {
		if budget < DigestSize && len(resp.Digests) > 0 {
			resp.NextShard = i
			break
		}
		resp.Digests = append(resp.Digests, snap.digests[i])
		budget -= DigestSize
	}
	resp.Bytes = DigestResponseHeader + len(resp.Digests)*DigestSize
	return resp
}

// HandlePull serves the ordered records of one shard after req.Cursor,
// limited by req.MaxBytes. The cursor is bound to req.SnapshotVersion; an
// unknown (invalidated) snapshot yields StatusRestart, never a skipped or
// shifted range.
func (s *Server) HandlePull(req PullRequest) PullResponse {
	s.mu.Lock()
	snap, ok := s.snaps[req.SnapshotVersion]
	s.mu.Unlock()
	if !ok || req.Shard < 0 || req.Shard >= len(snap.shards) {
		return PullResponse{Status: StatusRestart, Bytes: PullResponseHeader}
	}
	recs := snap.shards[req.Shard]
	start := sort.Search(len(recs), func(i int) bool { return recs[i].NodeID > req.Cursor })
	resp := PullResponse{Status: StatusOK, SnapshotVersion: snap.version}
	budget := req.MaxBytes - PullResponseHeader
	i := start
	for ; i < len(recs); i++ {
		sz := RecordSize(recs[i])
		if budget < sz && len(resp.Records) > 0 {
			break
		}
		budget -= sz // always emit at least one record: guarantees progress
		resp.Records = append(resp.Records, recs[i])
	}
	if i >= len(recs) {
		resp.Done = true
	} else {
		resp.NextCursor = recs[i-1].NodeID
	}
	resp.Checksum = ChecksumRecords(resp.Records)
	resp.Bytes = PullResponseHeader
	for _, r := range resp.Records {
		resp.Bytes += RecordSize(r)
	}
	return resp
}
