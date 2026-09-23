package twopc

import (
	"fmt"
	"strings"

	"twopc-lab/sim"
)

// Cluster bundles a scheduler, one coordinator and a set of participants.
type Cluster struct {
	Sched *sim.Scheduler
	Coord *Coordinator
	Parts map[string]*Participant

	order []string
	log   strings.Builder
}

// NewCluster builds and deploys a cluster with clean links. coordID is the
// coordinator node id; partIDs are the participants.
func NewCluster(coordID string, partIDs []string, coordCfg CoordinatorConfig, partCfg ParticipantConfig) *Cluster {
	sched := sim.NewScheduler(sim.NewNetwork())
	coord := NewCoordinator(coordID, coordCfg)
	sched.Deploy(coordID, coord, nil)

	parts := map[string]*Participant{}
	for _, id := range partIDs {
		p := NewParticipant(id, coordID, partCfg)
		sched.Deploy(id, p, nil)
		parts[id] = p
	}
	cl := &Cluster{Sched: sched, Coord: coord, Parts: parts, order: append([]string(nil), partIDs...)}
	sched.Network().Observe = cl.observe
	return cl
}

// Boot starts every node at time 0.
func (cl *Cluster) Boot() {
	cl.Sched.StartNode(cl.Coord.ID)
	for _, id := range cl.order {
		cl.Sched.StartNode(id)
	}
}

// PartIDs returns participants in construction order.
func (cl *Cluster) PartIDs() []string { return append([]string(nil), cl.order...) }

// Run advances the virtual clock until horizon.
func (cl *Cluster) Run(horizon int64) int { return cl.Sched.Run(horizon) }

// StartTxn schedules a new transaction at time t.
func (cl *Cluster) StartTxn(t int64, txn string, participants, keys, values []string) {
	cl.Sched.Plan(t, cl.Coord.ID, func() {
		cl.Coord.RunTxn(txn, participants, keys, values)
	})
}

// TraceLog returns the text accumulated via message tracing.
func (cl *Cluster) TraceLog() string { return cl.log.String() }

func (cl *Cluster) observe(now int64, env sim.Envelope, dropped bool, copies int) {
	tag := ""
	if dropped {
		tag = " DROPPED"
	} else if copies > 1 {
		tag = fmt.Sprintf(" x%d", copies)
	}
	fmt.Fprintf(&cl.log, "t=%8d  %s%s\n", now, env.String(), tag)
}

// SafetyViolations collects violations across all nodes.
func (cl *Cluster) SafetyViolations() []SafetyEvent {
	var out []SafetyEvent
	out = append(out, cl.Coord.SafetyViolations()...)
	for _, id := range cl.order {
		out = append(out, cl.Parts[id].SafetyViolations()...)
	}
	return out
}
