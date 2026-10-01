package main

import "time"

// Stats summarizes detection quality for one matching result.
type Stats struct {
	Matched        int
	FalsePositives int // unmatched alerts
	FalseNegatives int // missed faults (resolved before obs end)
	RightCensored  int // faults still open at obs end, reported separately
	Detections     int // matched pairs with alert at/after fault start
	MeanLatency    time.Duration
	MaxLatency     time.Duration
	Leads          int // matched pairs with alert before fault start
	MeanLead       time.Duration
}

func computeStats(res MatchResult) Stats {
	s := Stats{
		Matched:        res.MatchedCount,
		FalsePositives: len(res.UnmatchedAlerts),
		FalseNegatives: len(res.MissedFaults),
		RightCensored:  len(res.CensoredFaults),
	}
	var latSum, leadSum time.Duration
	for _, p := range res.Pairs {
		if p.Latency >= 0 {
			s.Detections++
			latSum += p.Latency
			if p.Latency > s.MaxLatency {
				s.MaxLatency = p.Latency
			}
		} else {
			s.Leads++
			leadSum += -p.Latency
		}
	}
	if s.Detections > 0 {
		s.MeanLatency = latSum / time.Duration(s.Detections)
	}
	if s.Leads > 0 {
		s.MeanLead = leadSum / time.Duration(s.Leads)
	}
	return s
}
