package bgpsim

import (
	"fmt"
	"sort"
	"strings"
)

// Simulate runs the synchronous advertisement evolution described by cfg.
//
// Round 0 selects routes from stable external injections; round k consumes
// exactly the advertisements produced at the end of round k-1. Every node
// then advertises its current best route to all derived BGP peers. The
// run stops at a fixed point (StatusConverged) or at the first repeated
// full control state with period >= 2 (StatusOscillating).
func Simulate(cfg *Config) (*Result, error) {
	if cfg == nil {
		return nil, fmt.Errorf("bgpsim: nil Config")
	}

	nodes := cfg.nodeByID()
	peers, rrCluster, err := cfg.buildPeers(nodes)
	if err != nil {
		return nil, err
	}

	prefixSet := map[string]bool{}
	for _, r := range cfg.ExternalRoutes {
		prefixSet[r.Prefix] = true
	}
	prefixes := make([]string, 0, len(prefixSet))
	for p := range prefixSet {
		prefixes = append(prefixes, p)
	}
	sort.Strings(prefixes)

	if err := cfg.validate(nodes, rrCluster, prefixes); err != nil {
		return nil, err
	}

	// Normalize routes and group stable external injections.
	origins := make(map[NodeID]map[string][]Route)
	for _, in := range cfg.ExternalRoutes {
		r := in.clone()
		if node, ok := nodes[r.Egress]; ok && r.RouterID == "" {
			r.RouterID = node.RouterID
		}
		r.Local = true
		r.LearnedFrom = ""
		origins[r.Egress] = ensurePrefixMap(origins[r.Egress])
		origins[r.Egress][r.Prefix] = append(origins[r.Egress][r.Prefix], r)
	}

	maxRounds := cfg.Options.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 256
	}

	peerList := make(map[NodeID][]NodeID, len(peers))
	for id, set := range peers {
		list := make([]NodeID, 0, len(set))
		for p := range set {
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		peerList[id] = list
	}

	res := &Result{
		AlwaysCompareMED:            cfg.Options.AlwaysCompareMED,
		Peers:                       peerList,
		Best:                        map[NodeID]map[string]*NodeChoice{},
		MEDIncomparableEliminations: []EliminationMark{},
		ClusterLoopDiscards:         []DiscardEvent{},
	}

	nodeIDs := cfg.sortedNodeIDs()

	// inbox[receiver][sender][prefix] = latest advertisement
	inbox := map[NodeID]map[NodeID]map[string]Route{}
	seen := map[string]int{}

	var finalChoices map[NodeID]map[string]*NodeChoice

	for round := 0; round < maxRounds; round++ {
		report := &RoundReport{
			Round:   round,
			Choices: map[NodeID]map[string]*NodeChoice{},
		}

		for _, id := range nodeIDs {
			report.Choices[id] = map[string]*NodeChoice{}
			res.Best[id] = map[string]*NodeChoice{}
			for _, prefix := range prefixes {
				choice := cfg.selectChoice(selectionInput{
					node:      id,
					prefix:    prefix,
					round:     round,
					locals:    origins[id][prefix],
					inbox:     inbox[id],
					clusterID: rrCluster[id],
					report:    report,
					result:    res,
				})
				report.Choices[id][prefix] = choice
			}
		}

		fp := fingerprint(report, nodeIDs, prefixes)
		report.Fingerprint = fp

		if first, ok := seen[fp]; ok {
			report.Repeated = true
			report.EqualsRound = first
			res.Rounds = append(res.Rounds, report)
			finalChoices = report.Choices

			period := round - first
			if period == 1 {
				res.Status = StatusConverged
				res.Converged = true
			} else {
				res.Status = StatusOscillating
				cycle := make([]*RoundReport, 0, period)
				for k := first; k < round; k++ {
					cycle = append(cycle, res.Rounds[k])
				}
				res.Oscillation = &Oscillation{
					FirstRound:    first,
					RepeatRound:   round,
					Period:        period,
					FirstState:    res.Rounds[first],
					RepeatedState: report,
					Cycle:         cycle,
				}
			}
			break
		}
		seen[fp] = round
		res.Rounds = append(res.Rounds, report)
		finalChoices = report.Choices

		// Synchronous advertisement phase: build next round's inbox.
		nextInbox := map[NodeID]map[NodeID]map[string]Route{}
		for _, id := range nodeIDs {
			for _, prefix := range prefixes {
				choice := report.Choices[id][prefix]
				if choice == nil || choice.Best == nil {
					continue
				}
				ad := choice.Best.clone()
				if cid, isRR := rrCluster[id]; isRR && cid != "" && !ad.Local {
					ad.ClusterList = appendCluster(ad.ClusterList, cid)
				}
				ad.Local = false
				for _, peer := range peerList[id] {
					if nextInbox[peer] == nil {
						nextInbox[peer] = map[NodeID]map[string]Route{}
					}
					if nextInbox[peer][id] == nil {
						nextInbox[peer][id] = map[string]Route{}
					}
					nextInbox[peer][id][prefix] = ad
				}
			}
		}
		inbox = nextInbox
	}

	if res.Status == "" {
		res.Status = StatusLimitReached
		if len(res.Rounds) > 0 {
			finalChoices = res.Rounds[len(res.Rounds)-1].Choices
		}
	}

	if finalChoices != nil {
		for id, byPrefix := range finalChoices {
			for prefix, choice := range byPrefix {
				res.Best[id][prefix] = choice
			}
		}
	}

	return res, nil
}

func ensurePrefixMap(m map[string][]Route) map[string][]Route {
	if m == nil {
		return map[string][]Route{}
	}
	return m
}

type selectionInput struct {
	node      NodeID
	prefix    string
	round     int
	locals    []Route
	inbox     map[NodeID]map[string]Route
	clusterID ClusterID
	report    *RoundReport
	result    *Result
}

type candidate struct {
	route Route
	eval  CandidateEvaluation
}

func (cfg *Config) igpCost(from NodeID, r Route) int {
	if from == r.Egress {
		return 0
	}
	if row, ok := cfg.IGP[from]; ok {
		if c, ok := row[r.Egress]; ok {
			return c
		}
	}
	// Validation normally makes this unreachable.
	return 1 << 30
}

// selectChoice runs the deterministic sequential best-path algorithm.
func (cfg *Config) selectChoice(in selectionInput) *NodeChoice {
	choice := &NodeChoice{Node: in.node, Prefix: in.prefix}

	// Stable external-injection tier: the origin border always keeps and
	// exports its locally received best external route (eBGP precedence).
	if len(in.locals) > 0 {
		cs := make([]*candidate, 0, len(in.locals))
		for _, r := range in.locals {
			r.Local = true
			cs = append(cs, &candidate{route: r})
		}
		canonicalCandidateOrder(cs)
		best := cfg.fold(cs, in, choice, false)
		finishChoice(cfg, in, choice, cs, best)
		return choice
	}

	cs := []*candidate{}
	senders := make([]NodeID, 0, len(in.inbox))
	for s := range in.inbox {
		senders = append(senders, s)
	}
	sort.Slice(senders, func(i, j int) bool { return senders[i] < senders[j] })

	for _, sender := range senders {
		ad, ok := in.inbox[sender][in.prefix]
		if !ok {
			continue
		}
		if in.clusterID != "" && containsCluster(ad.ClusterList, in.clusterID) {
			ev := DiscardEvent{
				Round:       in.round,
				Node:        in.node,
				From:        sender,
				Prefix:      in.prefix,
				Reason:      DiscardClusterLoop,
				ClusterList: append([]ClusterID(nil), ad.ClusterList...),
			}
			in.report.Events = append(in.report.Events, ev)
			in.result.ClusterLoopDiscards = append(in.result.ClusterLoopDiscards, ev)
			continue
		}
		r := ad.clone()
		r.LearnedFrom = sender
		r.Local = false
		cs = append(cs, &candidate{route: r})
	}

	if len(cs) == 0 {
		return choice
	}

	canonicalCandidateOrder(cs)
	best := cfg.fold(cs, in, choice, true)
	finishChoice(cfg, in, choice, cs, best)
	return choice
}

// fold performs the sequential "new candidate versus current best"
// comparison and records elimination marks. It returns the winning index.
func (cfg *Config) fold(cs []*candidate, in selectionInput, choice *NodeChoice, allowMarks bool) int {
	bestIdx := 0
	cs[0].eval.Order = 0

	for i := 1; i < len(cs); i++ {
		cur := cs[i]
		best := cs[bestIdx]

		xCost := cfg.igpCost(in.node, cur.route)
		yCost := cfg.igpCost(in.node, best.route)
		v := Compare(cur.route, best.route, xCost, yCost, cfg.Options.AlwaysCompareMED)

		switch {
		case v.Better < 0:
			// cur (x) beats the incumbent best (y): dethrone incumbent.
			cfg.markEliminated(best, cur.route, v, in, allowMarks)
			bestIdx = i
		case v.Better > 0:
			// cur loses to incumbent.
			cfg.markEliminated(cur, best.route, v, in, allowMarks)
		default:
			// Metric tie: deterministic propagation tie break.
			if propagationPreferred(cur.route, best.route) {
				tieVerdict := v
				tieVerdict.Stage = StageTie
				cfg.markEliminated(best, cur.route, tieVerdict, in, false)
				bestIdx = i
			} else {
				tieVerdict := v
				tieVerdict.Stage = StageTie
				cfg.markEliminated(cur, best.route, tieVerdict, in, false)
			}
		}
	}
	return bestIdx
}

func (cfg *Config) markEliminated(loser *candidate, winner Route, v Verdict, in selectionInput, allowMarks bool) {
	winnerCopy := winner.clone()
	loser.eval.Selected = false
	loser.eval.EliminatedAt = v.Stage
	loser.eval.LostTo = &winnerCopy
	loser.eval.MEDCompared = v.MEDCompared
	loser.eval.MEDIncomparable = v.MEDIncomparable
	loser.eval.LowerMEDThanRival = v.MEDIncomparable && loser.route.MED < winner.MED

	if allowMarks && v.MEDIncomparable && v.Stage != StageTie {
		in.result.MEDIncomparableEliminations = append(in.result.MEDIncomparableEliminations, EliminationMark{
			Round:             in.round,
			Node:              in.node,
			Prefix:            in.prefix,
			Candidate:         loser.route.clone(),
			Winner:            winnerCopy,
			Stage:             v.Stage,
			CandidateLowerMED: loser.eval.LowerMEDThanRival,
		})
	}
}

// propagationPreferred reports whether a should win a full metric tie
// against b using stable propagation state: local first, shorter
// CLUSTER_LIST, then lower advertising peer.
func propagationPreferred(a, b Route) bool {
	if a.Local != b.Local {
		return a.Local
	}
	if len(a.ClusterList) != len(b.ClusterList) {
		return len(a.ClusterList) < len(b.ClusterList)
	}
	if a.LearnedFrom != b.LearnedFrom {
		return a.LearnedFrom < b.LearnedFrom
	}
	return strings.Join(clusterStrings(a.ClusterList), ",") < strings.Join(clusterStrings(b.ClusterList), ",")
}

func clusterStrings(in []ClusterID) []string {
	out := make([]string, len(in))
	for i, c := range in {
		out[i] = string(c)
	}
	return out
}

func finishChoice(cfg *Config, in selectionInput, choice *NodeChoice, cs []*candidate, bestIdx int) {
	evals := make([]CandidateEvaluation, len(cs))
	for i, c := range cs {
		c.eval.Order = i
		c.eval.Route = c.route.clone()
		evals[i] = c.eval
	}

	bestRoute := cs[bestIdx].route
	bestCost := cfg.igpCost(in.node, bestRoute)
	for i := range evals {
		if i == bestIdx {
			// The winner is always selected even if it previously carried
			// an elimination evaluation (defensive; the fold never
			// re-selects a dethroned candidate).
			evals[i].Selected = true
			evals[i].EliminatedAt = ""
			evals[i].LostTo = nil
			continue
		}
		candRoute := cs[i].route
		v := Compare(candRoute, bestRoute, cfg.igpCost(in.node, candRoute), bestCost,
			cfg.Options.AlwaysCompareMED)
		if v.MEDIncomparable && v.Better > 0 {
			evals[i].IncomparabilityEliminated = true
		}
	}
	choice.Candidates = evals
	best := cs[bestIdx].route.clone()
	choice.Best = &best
}

func fingerprint(r *RoundReport, nodes []NodeID, prefixes []string) string {
	var b strings.Builder
	for _, id := range nodes {
		byPrefix := r.Choices[id]
		for _, p := range prefixes {
			c := byPrefix[p]
			b.WriteString(string(id))
			b.WriteByte('|')
			b.WriteString(p)
			b.WriteByte('=')
			if c == nil || c.Best == nil {
				b.WriteString("-")
			} else {
				rt := c.Best
				fmt.Fprintf(&b, "%s>%s>rid%s>as%d>med%d>lp%d>pl%d>o%d>loc%t>from%s>cl[",
					rt.Prefix, rt.Egress, rt.RouterID, rt.NeighborAS, rt.MED,
					rt.localPref(), rt.ASPathLen, rt.Origin, rt.Local, rt.LearnedFrom)
				for i, cid := range rt.ClusterList {
					if i > 0 {
						b.WriteByte(',')
					}
					b.WriteString(string(cid))
				}
				b.WriteByte(']')
			}
			b.WriteByte(';')
		}
	}
	return b.String()
}
