package syncx

import (
	"context"
	"errors"
	"fmt"

	"antientropy/member"
)

const (
	// MaxRestartsPerRound bounds snapshot restarts inside one round so a
	// flapping peer cannot keep a round alive forever.
	MaxRestartsPerRound = 4
	maxStallsPerShard   = 8
)

// ErrTooManyRestarts is returned when a round keeps hitting invalidated
// snapshots; the round fails and is retried on the next scheduled probe.
var ErrTooManyRestarts = errors.New("syncx: snapshot restarted too many times in one round")

var errRestart = errors.New("syncx: snapshot invalidated, restarting round")

// Transport abstracts the network so simulations can inject faults.
type Transport interface {
	Digest(ctx context.Context, to string, req DigestRequest) (DigestResponse, error)
	Pull(ctx context.Context, to string, req PullRequest) (PullResponse, error)
}

// CursorMark records one page of cursor progress for statistics.
type CursorMark struct {
	Shard int
	From  string
	To    string
	Done  bool
}

// RoundStats accounts one sync round: bytes, pages, records and cursor
// progress.
type RoundStats struct {
	Peer            string
	RequestBytes    int
	ResponseBytes   int
	Pages           int
	RecordsPulled   int
	RecordsApplied  int
	Restarts        int
	ForgeryDetected int
	CursorProgress  []CursorMark
}

// TotalBytes is the wire cost of the round.
func (s *RoundStats) TotalBytes() int { return s.RequestBytes + s.ResponseBytes }

// Client runs anti-entropy rounds against peers.
type Client struct {
	ID           string
	Srv          *Server
	MaxRespBytes int
	// ForceFullEvery > 0 forces a full pull of every shard every Nth round
	// with a peer. This bounds how long a forged "matching" digest can hide
	// divergence, independently of digest honesty.
	ForceFullEvery int

	roundsWithPeer map[string]int
}

func NewClient(id string, srv *Server, maxRespBytes int) *Client {
	return &Client{ID: id, Srv: srv, MaxRespBytes: maxRespBytes, roundsWithPeer: map[string]int{}}
}

// SyncRound runs one anti-entropy round: exchange paginated shard digests,
// then pull only the ordered record ranges of shards whose digests disagree.
func (c *Client) SyncRound(ctx context.Context, tr Transport, peer string) (RoundStats, error) {
	var st RoundStats
	st.Peer = peer
	c.roundsWithPeer[peer]++
	forceFull := c.ForceFullEvery > 0 && c.roundsWithPeer[peer]%c.ForceFullEvery == 0
	for {
		if st.Restarts > MaxRestartsPerRound {
			return st, ErrTooManyRestarts
		}
		err := c.syncAttempt(ctx, tr, peer, forceFull, &st)
		if err == errRestart {
			st.Restarts++
			continue
		}
		return st, err
	}
}

func (c *Client) syncAttempt(ctx context.Context, tr Transport, peer string, forceFull bool, st *RoundStats) error {
	// Phase 1: paginated digest exchange. The snapshot version of the first
	// page pins the whole round; a version change restarts the exchange.
	var digests []Digest
	var snapVersion uint64
	numShards := 0
	start := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		st.RequestBytes += DigestRequestBytes
		resp, err := tr.Digest(ctx, peer, DigestRequest{StartShard: start, MaxBytes: c.MaxRespBytes})
		if err != nil {
			return err
		}
		st.ResponseBytes += resp.Bytes
		if len(digests) == 0 {
			snapVersion = resp.SnapshotVersion
			numShards = resp.NumShards
		} else if resp.SnapshotVersion != snapVersion {
			return errRestart
		}
		digests = append(digests, resp.Digests...)
		if resp.NextShard < 0 {
			break
		}
		if resp.NextShard <= start && len(resp.Digests) == 0 {
			return fmt.Errorf("syncx: peer %s: digest exchange not advancing", peer)
		}
		start = resp.NextShard
	}

	// Phase 2: pull only shards whose digest disagrees. Local digests are
	// computed under the peer's sharding so mixed-config rounds still work.
	local := c.Srv.DigestsFor(numShards)
	for _, d := range digests {
		if d.Shard < 0 || d.Shard >= len(local) {
			continue
		}
		if !forceFull && local[d.Shard] == d {
			continue
		}
		if err := c.pullShard(ctx, tr, peer, d, snapVersion, st); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) pullShard(ctx context.Context, tr Transport, peer string, d Digest, snapVersion uint64, st *RoundStats) error {
	cursor := ""
	stalls := 0
	received := map[string]member.Record{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		st.RequestBytes += PullRequestBaseBytes + len(cursor)
		resp, err := tr.Pull(ctx, peer, PullRequest{
			Shard:           d.Shard,
			SnapshotVersion: snapVersion,
			Cursor:          cursor,
			MaxBytes:        c.MaxRespBytes,
		})
		if err != nil {
			return err
		}
		st.ResponseBytes += resp.Bytes
		if resp.Status == StatusRestart || resp.SnapshotVersion != snapVersion {
			return errRestart
		}
		if ChecksumRecords(resp.Records) != resp.Checksum {
			return fmt.Errorf("syncx: peer %s shard %d: corrupt or incomplete page", peer, d.Shard)
		}
		if len(resp.Records) == 0 && !resp.Done {
			return fmt.Errorf("syncx: peer %s shard %d: empty non-final page", peer, d.Shard)
		}
		st.Pages++
		st.RecordsPulled += len(resp.Records)
		// Merge, never overwrite: a stale page cannot clobber a newer local
		// incarnation because Table.Apply joins in the (incarnation, state)
		// lattice.
		st.RecordsApplied += c.Srv.Table.Apply(resp.Records...)
		for _, r := range resp.Records {
			received[r.NodeID] = r
		}
		from := cursor
		if resp.Done {
			st.CursorProgress = append(st.CursorProgress, CursorMark{Shard: d.Shard, From: from, To: cursor, Done: true})
			break
		}
		if resp.NextCursor <= cursor {
			// Duplicate or replayed page: applying it was harmless
			// (idempotent) but it must not move the cursor backwards.
			stalls++
			if stalls > maxStallsPerShard {
				return fmt.Errorf("syncx: peer %s shard %d: cursor not advancing", peer, d.Shard)
			}
			continue
		}
		stalls = 0
		cursor = resp.NextCursor
		st.CursorProgress = append(st.CursorProgress, CursorMark{Shard: d.Shard, From: from, To: cursor})
	}
	// Post-pull verification: the union of all pages of this shard must hash
	// to the advertised digest. A mismatch means the digest was forged (or
	// the peer violated the protocol); the scheduler is notified via
	// ForgeryDetected and demotes the peer's trust.
	recs := make([]member.Record, 0, len(received))
	for _, r := range received {
		recs = append(recs, r)
	}
	if ComputeDigest(recs, d.Shard, d.NumShards) != d {
		st.ForgeryDetected++
	}
	return nil
}
