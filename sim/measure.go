package sim

import "math"

func (s *Simulator) measure() *Result {
	measureSpan := s.cfg.Duration - s.cfg.Warmup
	if measureSpan <= 0 {
		measureSpan = s.cfg.Duration
	}
	r := &Result{
		Metrics:         map[string]*NodeMetrics{},
		PhysicalRate:    map[string]float64{},
		LogicalRate:     map[string]float64{},
		FailProb:        map[string]float64{},
		LoadRatio:       map[string]float64{},
		Amplification:   map[string]float64{},
		Avalanche:       map[string]bool{},
		MeasureDuration: measureSpan,
	}
	for _, ncfg := range s.cfg.Nodes {
		m := s.metrics[ncfg.Name]
		cp := *m
		r.Metrics[ncfg.Name] = &cp

		phys := float64(m.Attempts) / measureSpan
		logical := float64(m.LogicalArrivals) / measureSpan
		r.PhysicalRate[ncfg.Name] = phys
		r.LogicalRate[ncfg.Name] = logical

		total := m.Successes + m.Failures
		if total > 0 {
			r.FailProb[ncfg.Name] = float64(m.Failures) / float64(total)
		}
		if ncfg.Capacity > 0 {
			r.LoadRatio[ncfg.Name] = phys / ncfg.Capacity
			r.Avalanche[ncfg.Name] = phys > ncfg.Capacity
		}
		if logical > 0 {
			r.Amplification[ncfg.Name] = phys / logical
		} else {
			r.Amplification[ncfg.Name] = 0
		}
	}
	return r
}

// RelativeError 返回仿真与求解器在物理速率上的最大相对误差（仅对给定节点）。
func RelativeError(simRate, solverRate float64) float64 {
	base := math.Max(math.Abs(solverRate), 1e-9)
	return math.Abs(simRate-solverRate) / base
}
