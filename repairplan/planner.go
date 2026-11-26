package repairplan

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Failure reason reported when a missing shard has no legal target OSD.
const (
	// ReasonNoOnlineOSD: no effectively-online OSD can host the shard.
	ReasonNoOnlineOSD = "NO_ONLINE_OSD"
	// ReasonCapacityExhausted: every eligible online OSD lacks capacity.
	ReasonCapacityExhausted = "CAPACITY_EXHAUSTED"
	// ReasonDomainExhausted: all capacity-ready online OSDs collide with the
	// stripe on the mandatory fault-domain ancestor.
	ReasonDomainExhausted = "FAULT_DOMAIN_EXHAUSTED"
)

// Options controls global scheduler behavior.
type Options struct {
	// GlobalConcurrent caps repair tasks running in a single round. A value
	// <= 0 means no global cap.
	GlobalConcurrent int
	// RoundDuration is the wall-clock length of one scheduling round. It
	// only affects completion timestamps; defaults to one second.
	RoundDuration time.Duration
}

// PlacementFailure explains why one missing shard cannot be placed without
// violating the placement rules.
type PlacementFailure struct {
	StripeID    string
	ShardIndex  int
	Reason      string
	Detail      string
	FaultDomain NodeLevel
}

// RepairTask is one shard rebuild: reading k source shards and writing one
// rebuilt shard onto TargetOSD.
type RepairTask struct {
	StripeID         string
	ShardIndex       int
	TargetOSD        string
	SourceOSDs       []string
	CrossRackSources []string
	Bytes            int64
}

// NodeUsage is the aggregate repair traffic and task count observed on one
// topology node during a round.
type NodeUsage struct {
	IngressBytes    int64
	EgressBytes     int64
	ConcurrentTasks int
}

// ScheduledRound is one epoch of the repair timeline.
type ScheduledRound struct {
	Round     int
	Start     time.Duration
	End       time.Duration
	Tasks     []RepairTask
	NodeUsage map[string]NodeUsage
}

// StripeReport is the per-stripe planning verdict and completion timeline.
type StripeReport struct {
	StripeID       string
	Status         StripeStatus
	Margin         int
	Surviving      int
	MissingShards  []int
	Tasks          []RepairTask
	Failures       []PlacementFailure
	Scheduled      bool
	CompletedRound int
	CompletedAt    time.Duration
}

// Plan is the full output of a planning run.
type Plan struct {
	Rounds        []ScheduledRound
	StripeReports []StripeReport
	// Unrecoverable lists stripes with fewer than k surviving shards.
	Unrecoverable []StripeClassification
	TotalRounds   int
}

// ErrNoProgress indicates pending tasks can never fit the configured
// bandwidth/concurrency limits.
var ErrNoProgress = errors.New("repairplan: no task could be scheduled within the bandwidth and concurrency limits")

type plannedStripe struct {
	stripe    Stripe
	class     StripeClassification
	tasks     []RepairTask
	failures  []PlacementFailure
	scheduled bool
}

type planner struct {
	topo      *Topology
	opts      Options
	reserved  map[string]int64 // bytes already reserved on each OSD by earlier stripes
	planned   []*plannedStripe
	unrecover []StripeClassification
}

// BuildPlan runs the full classification, placement and scheduling pipeline.
func BuildPlan(topo *Topology, stripes []Stripe, opts Options) (*Plan, error) {
	if topo == nil {
		return nil, errors.New("repairplan: topology is required")
	}
	if len(stripes) == 0 {
		return nil, errors.New("repairplan: at least one stripe is required")
	}
	if opts.RoundDuration <= 0 {
		opts.RoundDuration = time.Second
	}
	p := &planner{
		topo:     topo,
		opts:     opts,
		reserved: map[string]int64{},
	}

	degraded := make([]Stripe, 0, len(stripes))
	classes := map[string]StripeClassification{}
	for _, stripe := range stripes {
		if err := stripe.validate(); err != nil {
			return nil, err
		}
		class, err := classify(stripe, topo)
		if err != nil {
			return nil, err
		}
		switch class.Status {
		case StatusUnrecoverable:
			p.unrecover = append(p.unrecover, class)
		case StatusHealthy:
			// Nothing to repair; reported but never scheduled.
			p.planned = append(p.planned, &plannedStripe{
				stripe: stripe,
				class:  class,
			})
		default:
			degraded = append(degraded, stripe)
			classes[stripe.ID] = class
		}
	}

	// Priority: margin ascending (most endangered first), stripe size
	// descending, stripe ID lexicographic.
	sort.SliceStable(degraded, func(i, j int) bool {
		ci, cj := classes[degraded[i].ID], classes[degraded[j].ID]
		if ci.Margin != cj.Margin {
			return ci.Margin < cj.Margin
		}
		if ci.Size != cj.Size {
			return ci.Size > cj.Size
		}
		return degraded[i].ID < degraded[j].ID
	})

	for _, stripe := range degraded {
		ps := p.planStripe(stripe, classes[stripe.ID])
		p.planned = append(p.planned, ps)
	}

	rounds, completion, err := p.schedule()
	if err != nil {
		return nil, err
	}
	return p.buildPlan(rounds, completion), nil
}

// planStripe performs anti-affinity target placement and source selection.
func (p *planner) planStripe(stripe Stripe, class StripeClassification) *plannedStripe {
	ps := &plannedStripe{stripe: stripe, class: class}

	// Fault-domain ancestors already occupied by surviving shards.
	usedDomains := map[string]bool{}
	usedOSDs := map[string]bool{}
	survivors := []ShardPlacement{}
	for _, sh := range stripe.Shards {
		if !p.topo.EffectiveOnline(sh.OSDID) {
			continue
		}
		survivors = append(survivors, sh)
		usedOSDs[sh.OSDID] = true
		ancestor, err := p.topo.Ancestor(sh.OSDID, stripe.FaultDomain)
		if err != nil {
			ps.failures = append(ps.failures, PlacementFailure{
				StripeID: stripe.ID, Reason: ReasonDomainExhausted,
				Detail: err.Error(), FaultDomain: stripe.FaultDomain,
			})
			return ps
		}
		usedDomains[ancestor.ID] = true
	}

	reservedHere := []string{}
	for _, idx := range class.Missing {
		target, reason, detail := p.pickTarget(stripe, usedDomains, usedOSDs)
		if reason != "" {
			ps.failures = append(ps.failures, PlacementFailure{
				StripeID:    stripe.ID,
				ShardIndex:  idx,
				Reason:      reason,
				Detail:      detail,
				FaultDomain: stripe.FaultDomain,
			})
			continue
		}
		sources := p.selectSources(stripe, survivors, target)
		ps.tasks = append(ps.tasks, RepairTask{
			StripeID:         stripe.ID,
			ShardIndex:       idx,
			TargetOSD:        target,
			SourceOSDs:       sources,
			CrossRackSources: p.crossRackSources(target, sources),
			Bytes:            stripe.ShardSize,
		})
		ancestor, _ := p.topo.Ancestor(target, stripe.FaultDomain)
		usedDomains[ancestor.ID] = true
		usedOSDs[target] = true
		p.reserved[target] += stripe.ShardSize
		reservedHere = append(reservedHere, target)
	}

	if len(ps.failures) > 0 {
		// Never place partially: release this stripe's reservations so it
		// does not consume capacity of later stripes.
		for _, id := range reservedHere {
			p.reserved[id] -= stripe.ShardSize
		}
		ps.tasks = nil
		ps.scheduled = false
		return ps
	}
	ps.scheduled = true
	return ps
}

// pickTarget returns the best legal OSD for one missing shard, or a reason
// code explaining why no legal target exists.
func (p *planner) pickTarget(stripe Stripe, usedDomains, usedOSDs map[string]bool) (string, string, string) {
	type candidate struct {
		osdID    string
		capacity int64
	}
	var valid []candidate
	onlineAny, onlineCapacityReady := false, false
	for _, osdID := range p.topo.OSDs() {
		node, _ := p.topo.Node(osdID)
		if !p.topo.EffectiveOnline(osdID) {
			continue
		}
		onlineAny = true
		available := node.RemainingCapacity
		if available != Unlimited {
			available -= p.reserved[osdID]
		}
		if available != Unlimited && available < stripe.ShardSize {
			continue
		}
		onlineCapacityReady = true
		ancestor, err := p.topo.Ancestor(osdID, stripe.FaultDomain)
		if err != nil {
			return "", ReasonDomainExhausted, err.Error()
		}
		if usedDomains[ancestor.ID] || usedOSDs[osdID] {
			continue
		}
		valid = append(valid, candidate{osdID: osdID, capacity: available})
	}
	switch {
	case !onlineAny:
		return "", ReasonNoOnlineOSD, "no effectively-online OSD exists in the topology"
	case !onlineCapacityReady:
		return "", ReasonCapacityExhausted, "every online OSD lacks free capacity for a shard"
	case len(valid) == 0:
		return "", ReasonDomainExhausted, fmt.Sprintf(
			"every capacity-ready online OSD shares an occupied %s ancestor or OSD with the stripe",
			stripe.FaultDomain)
	}
	sort.Slice(valid, func(i, j int) bool {
		ci, cj := valid[i].capacity, valid[j].capacity
		// Unlimited capacity sorts as larger than any finite value.
		if ci != cj {
			if ci == Unlimited {
				return true
			}
			if cj == Unlimited {
				return false
			}
			return ci > cj
		}
		return valid[i].osdID < valid[j].osdID
	})
	return valid[0].osdID, "", ""
}

// selectSources chooses exactly k surviving shards, maximizing reads from
// the target rack to minimize cross-rack traffic. Ties break on OSD ID.
func (p *planner) selectSources(stripe Stripe, survivors []ShardPlacement, targetOSD string) []string {
	targetRack := p.topo.RackOf(targetOSD)
	var sameRack, otherRack []string
	for _, sh := range survivors {
		if p.topo.RackOf(sh.OSDID) == targetRack {
			sameRack = append(sameRack, sh.OSDID)
		} else {
			otherRack = append(otherRack, sh.OSDID)
		}
	}
	sort.Strings(sameRack)
	sort.Strings(otherRack)
	chosen := append(sameRack, otherRack...)
	if len(chosen) < stripe.K {
		// Classification guarantees at least k survivors; defensive guard.
		panic(fmt.Sprintf("repairplan: stripe %q has only %d sources but needs %d",
			stripe.ID, len(chosen), stripe.K))
	}
	chosen = chosen[:stripe.K]
	sort.Strings(chosen)
	return chosen
}

func (p *planner) crossRackSources(targetOSD string, sources []string) []string {
	targetRack := p.topo.RackOf(targetOSD)
	var cross []string
	for _, src := range sources {
		if p.topo.RackOf(src) != targetRack {
			cross = append(cross, src)
		}
	}
	sort.Strings(cross)
	return cross
}

func (p *planner) buildPlan(rounds []ScheduledRound, completion map[string]roundCompletion) *Plan {
	plan := &Plan{
		Rounds:        rounds,
		Unrecoverable: p.unrecover,
		TotalRounds:   len(rounds),
	}
	for _, ps := range p.planned {
		report := StripeReport{
			StripeID:      ps.stripe.ID,
			Status:        ps.class.Status,
			Margin:        ps.class.Margin,
			Surviving:     ps.class.Surviving,
			MissingShards: append([]int(nil), ps.class.Missing...),
			Tasks:         append([]RepairTask(nil), ps.tasks...),
			Failures:      append([]PlacementFailure(nil), ps.failures...),
			Scheduled:     ps.scheduled,
		}
		if done, ok := completion[ps.stripe.ID]; ok {
			report.CompletedRound = done.round
			report.CompletedAt = done.at
		}
		plan.StripeReports = append(plan.StripeReports, report)
	}
	sort.SliceStable(plan.Unrecoverable, func(i, j int) bool {
		return plan.Unrecoverable[i].StripeID < plan.Unrecoverable[j].StripeID
	})
	return plan
}

type roundCompletion struct {
	round int
	at    time.Duration
}
