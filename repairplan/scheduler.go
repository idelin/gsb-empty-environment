package repairplan

import (
	"fmt"
	"time"
)

type roundState struct {
	ingress     map[string]int64
	egress      map[string]int64
	concurrency map[string]int
	globalTasks int
}

func newRoundState() *roundState {
	return &roundState{
		ingress:     map[string]int64{},
		egress:      map[string]int64{},
		concurrency: map[string]int{},
	}
}

// schedule simulates round-by-round bandwidth scheduling. Tasks are offered
// in stripe-priority order and shard-index order; a task only runs when
// every involved OSD/rack/... node keeps headroom under its ingress, egress
// and concurrency limits.
func (p *planner) schedule() ([]ScheduledRound, map[string]roundCompletion, error) {
	type pending struct {
		ps   *plannedStripe
		task RepairTask
	}
	var queue []pending
	taskTotal := map[string]int{}
	for _, ps := range p.planned {
		if !ps.scheduled {
			continue
		}
		for _, task := range ps.tasks {
			queue = append(queue, pending{ps: ps, task: task})
			taskTotal[ps.stripe.ID]++
		}
	}

	completion := map[string]roundCompletion{}
	done := map[string]int{}
	var rounds []ScheduledRound
	roundNum := 0
	for len(queue) > 0 {
		state := newRoundState()
		scheduled := []RepairTask{}
		remaining := make([]pending, 0, len(queue))
		for _, item := range queue {
			if p.fits(item.task, state) {
				p.charge(item.task, state)
				scheduled = append(scheduled, item.task)
				id := item.ps.stripe.ID
				done[id]++
				if done[id] == taskTotal[id] {
					completion[id] = roundCompletion{
						round: roundNum + 1,
						at:    time.Duration(roundNum+1) * p.opts.RoundDuration,
					}
				}
			} else {
				remaining = append(remaining, item)
			}
		}
		if len(scheduled) == 0 {
			return nil, nil, fmt.Errorf("%w: %d task(s) blocked by node bandwidth/concurrency limits",
				ErrNoProgress, len(queue))
		}
		start := time.Duration(roundNum) * p.opts.RoundDuration
		rounds = append(rounds, ScheduledRound{
			Round:     roundNum + 1,
			Start:     start,
			End:       start + p.opts.RoundDuration,
			Tasks:     scheduled,
			NodeUsage: p.snapshotUsage(state),
		})
		queue = remaining
		roundNum++
	}
	return rounds, completion, nil
}

// fits reports whether the task can be added without breaching any limit.
func (p *planner) fits(task RepairTask, st *roundState) bool {
	if p.opts.GlobalConcurrent > 0 && st.globalTasks >= p.opts.GlobalConcurrent {
		return false
	}
	if !p.nodeConcurrencyOK(task, st) {
		return false
	}
	bytes := task.Bytes
	ingressNodes, egressNodes := p.trafficNodes(task)
	for id := range ingressNodes {
		node, _ := p.topo.Node(id)
		if !ingressFits(node, st.ingress[id], bytes) {
			return false
		}
	}
	for id := range egressNodes {
		node, _ := p.topo.Node(id)
		if !egressFits(node, st.egress[id], bytes) {
			return false
		}
	}
	return true
}

// nodeConcurrencyOK checks MaxConcurrent on every distinct node the task
// touches (sources, target and their ancestors).
func (p *planner) nodeConcurrencyOK(task RepairTask, st *roundState) bool {
	touched := map[string]*Node{}
	for _, node := range p.topo.path(task.TargetOSD) {
		touched[node.ID] = node
	}
	for _, src := range task.SourceOSDs {
		for _, node := range p.topo.path(src) {
			touched[node.ID] = node
		}
	}
	for _, node := range touched {
		if node.MaxConcurrent > 0 && st.concurrency[node.ID] >= node.MaxConcurrent {
			return false
		}
	}
	return true
}

func ingressFits(node *Node, used, bytes int64) bool {
	if node.IngressLimit == Unlimited {
		return true
	}
	return used+bytes <= node.IngressLimit
}

func egressFits(node *Node, used, bytes int64) bool {
	if node.EgressLimit == Unlimited {
		return true
	}
	return used+bytes <= node.EgressLimit
}

// charge commits the task's bandwidth and concurrency usage to the round.
func (p *planner) charge(task RepairTask, st *roundState) {
	st.globalTasks++
	touched := map[string]bool{}
	for _, node := range p.topo.path(task.TargetOSD) {
		touched[node.ID] = true
	}
	for _, src := range task.SourceOSDs {
		for _, node := range p.topo.path(src) {
			touched[node.ID] = true
		}
	}
	for id := range touched {
		st.concurrency[id]++
	}

	ingressNodes, egressNodes := p.trafficNodes(task)
	for id := range ingressNodes {
		st.ingress[id] += task.Bytes
	}
	for id := range egressNodes {
		st.egress[id] += task.Bytes
	}
}

// trafficNodes returns the node sets carrying ingress (toward the target)
// and egress (away from a source) for a rebuild task. A node is counted at
// most once per task regardless of how many source flows traverse it, which
// models the aggregate per-link bytes of the single rebuilt-shard transfer.
func (p *planner) trafficNodes(task RepairTask) (ingress, egress map[string]bool) {
	ingress = map[string]bool{}
	egress = map[string]bool{}
	targetPath := p.topo.path(task.TargetOSD)
	for _, src := range task.SourceOSDs {
		srcPath := p.topo.path(src)
		lca := lcaLevel(targetPath, srcPath)
		for _, node := range targetPath {
			if levelOrder[node.Level] > lca {
				ingress[node.ID] = true
			}
		}
		for _, node := range srcPath {
			if levelOrder[node.Level] > lca {
				egress[node.ID] = true
			}
		}
	}
	return ingress, egress
}

// lcaLevel returns the level of the deepest common node of two root paths.
func lcaLevel(a, b []*Node) int {
	deepest := levelOrder[LevelRoot]
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].ID == b[i].ID {
			deepest = levelOrder[a[i].Level]
		} else {
			break
		}
	}
	return deepest
}

func (p *planner) snapshotUsage(st *roundState) map[string]NodeUsage {
	out := make(map[string]NodeUsage, len(st.concurrency))
	ids := map[string]bool{}
	for id := range st.ingress {
		ids[id] = true
	}
	for id := range st.egress {
		ids[id] = true
	}
	for id := range st.concurrency {
		ids[id] = true
	}
	for id := range ids {
		out[id] = NodeUsage{
			IngressBytes:    st.ingress[id],
			EgressBytes:     st.egress[id],
			ConcurrentTasks: st.concurrency[id],
		}
	}
	return out
}
