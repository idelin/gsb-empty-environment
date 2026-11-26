package repairplan

import (
	"errors"
	"fmt"
)

// StripeStatus is the precise health classification of a stripe.
type StripeStatus string

const (
	// StatusUnrecoverable: fewer than k shards survive; never scheduled.
	StatusUnrecoverable StripeStatus = "UNRECOVERABLE"
	// StatusDegraded: at least k but fewer than k+m shards survive;
	// the stripe is scheduled for repair.
	StatusDegraded StripeStatus = "DEGRADED"
	// StatusHealthy: all k+m shards survive on effectively-online OSDs.
	StatusHealthy StripeStatus = "HEALTHY"
)

// ShardPlacement records where one shard of a stripe currently lives.
type ShardPlacement struct {
	Index int
	OSDID string
}

// Stripe describes one erasure-coded or replicated placement group.
//
// A replicated stripe is represented with k=1 and m=replicas-1, so the
// total shard count is k+m. FaultDomain is the mandatory anti-affinity
// level (e.g. rack or host) enforced for target placement.
type Stripe struct {
	ID          string
	K           int
	M           int
	ShardSize   int64
	FaultDomain NodeLevel
	Shards      []ShardPlacement
}

// NewReplicatedStripe builds a stripe for a replicated placement group with
// the given number of replicas. The resulting stripe has k=1 and
// m=replicas-1.
func NewReplicatedStripe(id string, replicas int, shardSize int64, domain NodeLevel, shards []ShardPlacement) Stripe {
	return Stripe{
		ID:          id,
		K:           1,
		M:           replicas - 1,
		ShardSize:   shardSize,
		FaultDomain: domain,
		Shards:      shards,
	}
}

// TotalShards returns k+m, the full shard count of the stripe.
func (s Stripe) TotalShards() int { return s.K + s.M }

// Size returns the logical repair payload size of the stripe, i.e. the
// number of missing shards times the shard size. Full stripe size used for
// tie-breaking is TotalShards * ShardSize.
func (s Stripe) Size() int64 { return int64(s.TotalShards()) * s.ShardSize }

// validate checks structural invariants of the stripe definition.
func (s Stripe) validate() error {
	if s.ID == "" {
		return errors.New("repairplan: stripe has empty ID")
	}
	if s.K <= 0 || s.M <= 0 {
		return fmt.Errorf("repairplan: stripe %q requires positive k and m", s.ID)
	}
	if s.ShardSize <= 0 {
		return fmt.Errorf("repairplan: stripe %q requires positive shard size", s.ID)
	}
	if _, ok := levelOrder[s.FaultDomain]; !ok {
		return fmt.Errorf("repairplan: stripe %q has unknown fault domain %q", s.ID, s.FaultDomain)
	}
	if s.FaultDomain == LevelRoot || s.FaultDomain == LevelOSD {
		return fmt.Errorf("repairplan: stripe %q fault domain must be row, rack or host", s.ID)
	}
	if len(s.Shards) == 0 {
		return fmt.Errorf("repairplan: stripe %q has no shard placements", s.ID)
	}
	seenIndex := map[int]bool{}
	for _, sh := range s.Shards {
		if sh.Index < 0 || sh.Index >= s.TotalShards() {
			return fmt.Errorf("repairplan: stripe %q shard index %d out of range [0,%d)",
				s.ID, sh.Index, s.TotalShards())
		}
		if seenIndex[sh.Index] {
			return fmt.Errorf("repairplan: stripe %q lists shard %d more than once", s.ID, sh.Index)
		}
		seenIndex[sh.Index] = true
		if sh.OSDID == "" {
			return fmt.Errorf("repairplan: stripe %q shard %d has empty OSD ID", s.ID, sh.Index)
		}
	}
	return nil
}

// StripeClassification is the health verdict for one input stripe.
type StripeClassification struct {
	StripeID    string
	Status      StripeStatus
	K           int
	M           int
	TotalShards int
	Surviving   int
	Missing     []int
	// Margin is surviving-k: the number of additional shard losses the
	// stripe can still tolerate before becoming unrecoverable.
	Margin    int
	ShardSize int64
	Size      int64
}

// classify computes the health verdict for a stripe against the topology.
// Shards on effectively-offline OSDs count as lost, and duplicate OSD
// placements for the same stripe are rejected.
func classify(stripe Stripe, topo *Topology) (StripeClassification, error) {
	c := StripeClassification{
		StripeID:    stripe.ID,
		K:           stripe.K,
		M:           stripe.M,
		TotalShards: stripe.TotalShards(),
		ShardSize:   stripe.ShardSize,
		Size:        stripe.Size(),
	}
	alive := make([]bool, stripe.TotalShards())
	usedOSD := map[string]int{}
	for _, sh := range stripe.Shards {
		if _, ok := topo.Node(sh.OSDID); !ok {
			return c, fmt.Errorf("repairplan: stripe %q shard %d references unknown OSD %q",
				stripe.ID, sh.Index, sh.OSDID)
		}
		if prev, dup := usedOSD[sh.OSDID]; dup {
			return c, fmt.Errorf("repairplan: stripe %q places shards %d and %d on the same OSD %q",
				stripe.ID, prev, sh.Index, sh.OSDID)
		}
		usedOSD[sh.OSDID] = sh.Index
		if topo.EffectiveOnline(sh.OSDID) {
			alive[sh.Index] = true
			c.Surviving++
		}
	}
	for idx := 0; idx < stripe.TotalShards(); idx++ {
		if !alive[idx] {
			c.Missing = append(c.Missing, idx)
		}
	}
	c.Margin = c.Surviving - stripe.K
	switch {
	case c.Surviving < stripe.K:
		c.Status = StatusUnrecoverable
	case c.Surviving < stripe.TotalShards():
		c.Status = StatusDegraded
	default:
		c.Status = StatusHealthy
	}
	return c, nil
}
