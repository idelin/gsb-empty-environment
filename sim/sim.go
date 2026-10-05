// Package sim provides an in-memory cluster harness with scriptable
// transport faults for testing and replaying the anti-entropy protocol.
package sim

import (
	"context"
	"fmt"

	"antientropy/member"
	"antientropy/model"
	"antientropy/syncx"
)

// Node is one cluster member.
type Node struct {
	ID    string
	Srv   *syncx.Server
	Cli   *syncx.Client
	Sched *syncx.Scheduler
}

// Faults are transport-level fault-injection hooks. All hooks receive the
// current round and the (from, to) pair and run inside the simulated
// network, between client and server.
type Faults struct {
	// CorruptPage flips a page's checksum, simulating a truncated or
	// corrupted (incomplete) response.
	CorruptPage func(round int, from, to string) bool
	// ForgeDigest rewrites a digest in transit; the hook may both fake
	// divergence and hide real divergence.
	ForgeDigest func(round int, from, to string, d *syncx.Digest)
	// DuplicatePage replays the previous page instead of the requested one.
	DuplicatePage func(round int, from, to string) bool
	// CancelPull cancels the request before it reaches the server.
	CancelPull func(round int, from, to string) bool
	// BeforePull runs before the server handles a pull (e.g. to change the
	// server's shard config mid-round and invalidate its snapshots).
	BeforePull func(round int, from, to string)
	// BeforePullApply runs after the server built the response but before
	// the client applies it, racing local updates against stale pages.
	BeforePullApply func(round int, from, to string)
}

// Cluster is a simulated set of nodes with a fault-injecting network.
type Cluster struct {
	Nodes  map[string]*Node
	Order  []string
	Faults Faults
	Round  int
	// History logs every record ever introduced, in order. The model
	// checker full-merges it independently of the protocol.
	History []member.Record
	// LimitViolations counts responses that exceeded the byte budget
	// (single-record overflow pages are legal and not counted).
	LimitViolations int

	lastPullPage map[string]syncx.PullResponse
}

// NewCluster builds a fully-connected cluster. forceFullEvery is the
// client's forced-full-sync period (0 disables it).
func NewCluster(ids []string, numShards, maxRespBytes, forceFullEvery int) *Cluster {
	c := &Cluster{Nodes: map[string]*Node{}, lastPullPage: map[string]syncx.PullResponse{}}
	for _, id := range ids {
		srv := syncx.NewServer(numShards)
		cli := syncx.NewClient(id, srv, maxRespBytes)
		cli.ForceFullEvery = forceFullEvery
		c.Nodes[id] = &Node{ID: id, Srv: srv, Cli: cli}
		c.Order = append(c.Order, id)
	}
	for _, id := range ids {
		var peers []string
		for _, other := range ids {
			if other != id {
				peers = append(peers, other)
			}
		}
		c.Nodes[id].Sched = syncx.NewScheduler(peers)
	}
	return c
}

// Introduce applies records to one node and logs them to the global history.
func (c *Cluster) Introduce(node string, recs ...member.Record) {
	c.Nodes[node].Srv.Table.Apply(recs...)
	c.History = append(c.History, recs...)
}

// SetNumShards applies a shard-config change to every node.
func (c *Cluster) SetNumShards(n int) {
	for _, node := range c.Nodes {
		node.Srv.SetNumShards(n)
	}
}

// transport binds one caller to the simulated network.
type transport struct {
	cluster *Cluster
	from    string
}

func (t *transport) Digest(ctx context.Context, to string, req syncx.DigestRequest) (syncx.DigestResponse, error) {
	if err := ctx.Err(); err != nil {
		return syncx.DigestResponse{}, err
	}
	c := t.cluster
	resp := c.Nodes[to].Srv.HandleDigest(req)
	if c.Faults.ForgeDigest != nil {
		for i := range resp.Digests {
			c.Faults.ForgeDigest(c.Round, t.from, to, &resp.Digests[i])
		}
	}
	if resp.Bytes > req.MaxBytes && len(resp.Digests) > 1 {
		c.LimitViolations++
	}
	return resp, nil
}

func (t *transport) Pull(ctx context.Context, to string, req syncx.PullRequest) (syncx.PullResponse, error) {
	c := t.cluster
	if c.Faults.CancelPull != nil && c.Faults.CancelPull(c.Round, t.from, to) {
		return syncx.PullResponse{}, context.Canceled
	}
	if c.Faults.BeforePull != nil {
		c.Faults.BeforePull(c.Round, t.from, to)
	}
	key := t.from + "\x00" + to
	if c.Faults.DuplicatePage != nil && c.Faults.DuplicatePage(c.Round, t.from, to) {
		if prev, ok := c.lastPullPage[key]; ok {
			return prev, nil // replay a stale page
		}
	}
	resp := c.Nodes[to].Srv.HandlePull(req)
	if c.Faults.CorruptPage != nil && c.Faults.CorruptPage(c.Round, t.from, to) && len(resp.Records) > 0 {
		resp.Checksum++ // truncated/corrupted in transit
	}
	if resp.Bytes > req.MaxBytes && len(resp.Records) > 1 {
		c.LimitViolations++
	}
	c.lastPullPage[key] = resp
	if c.Faults.BeforePullApply != nil {
		c.Faults.BeforePullApply(c.Round, t.from, to)
	}
	return resp, nil
}

// RoundReport aggregates per-round statistics: bytes, records and cursor
// progress for every node.
type RoundReport struct {
	Round          int
	Stats          []syncx.RoundStats
	Errors         []string
	RequestBytes   int
	ResponseBytes  int
	RecordsPulled  int
	RecordsApplied int
	Restarts       int
	Forged         int
}

// TotalBytes is the wire cost of the round.
func (r RoundReport) TotalBytes() int { return r.RequestBytes + r.ResponseBytes }

// RunRound lets every node probe one peer (chosen by its fair scheduler).
// Round failures caused by faults never corrupt state; they surface as
// errors in the report and are retried by later rounds.
func (c *Cluster) RunRound(ctx context.Context) RoundReport {
	c.Round++
	rep := RoundReport{Round: c.Round}
	for _, id := range c.Order {
		node := c.Nodes[id]
		peer := node.Sched.Next()
		st, err := node.Cli.SyncRound(ctx, c.transport(id), peer)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("%s->%s: %v", id, peer, err))
		}
		if st.ForgeryDetected > 0 {
			// Demote, never ban: the scheduler still probes every peer
			// once per cycle.
			node.Sched.SetTrust(peer, 0.1)
		}
		rep.Stats = append(rep.Stats, st)
		rep.RequestBytes += st.RequestBytes
		rep.ResponseBytes += st.ResponseBytes
		rep.RecordsPulled += st.RecordsPulled
		rep.RecordsApplied += st.RecordsApplied
		rep.Restarts += st.Restarts
		rep.Forged += st.ForgeryDetected
	}
	return rep
}

func (c *Cluster) transport(from string) *transport { return &transport{cluster: c, from: from} }

// Converged reports whether every node's table equals the independent
// full-merge model of the whole history.
func (c *Cluster) Converged() bool { return c.CheckModel() == nil }

// CheckModel verifies every node against the independent full-merge model.
func (c *Cluster) CheckModel() error {
	want := model.FullMerge(c.History)
	for _, id := range c.Order {
		if err := model.CheckTable(c.Nodes[id].Srv.Table, want); err != nil {
			return fmt.Errorf("node %s: %w", id, err)
		}
	}
	return nil
}
