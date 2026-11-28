package steady

// buildResult converts raw warmup/total counters into per-second rates over
// the measurement window.
func (s *simulator) buildResult() *SimResult {
	windowSec := s.cfg.Duration.Seconds()
	res := &SimResult{
		nodeNormal:   make([]int64, len(s.topo.nodes)),
		nodePhysical: make([]int64, len(s.topo.nodes)),
		nodeFailure:  make([]int64, len(s.topo.nodes)),
		edgeCalls:    make([]int64, len(s.topo.edges)),
		edgeRetries:  make([]int64, len(s.topo.edges)),
		edgeHedges:   make([]int64, len(s.topo.edges)),
		edgeAttempts: make([]int64, len(s.topo.edges)),
	}
	for i, nd := range s.topo.nodes {
		normal := s.totalNormal[i] - s.warmNormal[i]
		physical := s.totalPhysical[i] - s.warmPhysical[i]
		failures := s.totalFailure[i] - s.warmFailure[i]
		res.nodeNormal[i] = normal
		res.nodePhysical[i] = physical
		res.nodeFailure[i] = failures

		normalRate := float64(normal) / windowSec
		physicalRate := float64(physical) / windowSec
		failRate := 0.0
		if physical > 0 {
			failRate = float64(failures) / float64(physical)
		}
		amp := 0.0
		if normalRate > 0 {
			amp = physicalRate / normalRate
		}
		loadCap := 0.0
		if nd.Capacity > 0 {
			loadCap = physicalRate / nd.Capacity
		}
		res.Nodes = append(res.Nodes, SimNodeResult{
			Name:            nd.Name,
			NormalArrival:   normalRate,
			PhysicalArrival: physicalRate,
			FailureRate:     failRate,
			Amplification:   amp,
			LoadToCapacity:  loadCap,
		})
	}
	for ei, e := range s.topo.edges {
		calls := s.totalCalls[ei] - s.warmCalls[ei]
		retries := s.totalRetries[ei] - s.warmRetries[ei]
		hedges := s.totalHedges[ei] - s.warmHedges[ei]
		attempts := s.totalEdgeAttempts[ei] - s.warmEdgeAttempts[ei]
		res.edgeCalls[ei] = calls
		res.edgeRetries[ei] = retries
		res.edgeHedges[ei] = hedges
		res.edgeAttempts[ei] = attempts
		callsF := float64(calls)
		rpc := 0.0
		hpc := 0.0
		if callsF > 0 {
			rpc = float64(retries) / callsF
			hpc = float64(hedges) / callsF
		}
		res.Edges = append(res.Edges, SimEdgeResult{
			From:           e.From,
			To:             e.To,
			Calls:          callsF / windowSec,
			AttemptRate:    float64(attempts) / windowSec,
			RetriesPerCall: rpc,
			HedgesPerCall:  hpc,
			ExtraPerCall:   rpc + hpc,
		})
	}
	return res
}
