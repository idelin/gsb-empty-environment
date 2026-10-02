package promlite

import "time"

// SynthBase is the deterministic base time for synthesized telemetry.
var SynthBase = time.Date(2026, 10, 2, 3, 30, 0, 0, time.UTC)

// Synth deterministically generates a local telemetry fixture:
//   - http_requests_total counters for jobs api (instances a, b) and web (c),
//     with a counter reset in api/b at the midpoint;
//   - machine_cores gauges, one series per job (for one-to-one division
//     after sum by (job)).
//
// No monitoring platform is involved; events are replayable via fixtures.
func Synth(steps int, step time.Duration) *Store {
	s := NewStore()
	base := SynthBase
	counters := []struct {
		labels    Labels
		start     float64
		inc       float64
		resetStep int // -1: never
	}{
		{Labels{"job": "api", "instance": "a"}, 1000, 30, -1},
		{Labels{"job": "api", "instance": "b"}, 2000, 50, steps / 2},
		{Labels{"job": "web", "instance": "c"}, 500, 20, -1},
	}
	for i := 0; i <= steps; i++ {
		ts := base.Add(time.Duration(i) * step)
		for _, c := range counters {
			v := c.start + float64(i)*c.inc
			if i == c.resetStep {
				v = 5 // counter reset (process restart)
			} else if c.resetStep >= 0 && i > c.resetStep {
				v = 5 + float64(i-c.resetStep)*c.inc
			}
			s.Add(Sample{Metric: "http_requests_total", Labels: copyLabels(c.labels), T: ts, V: v})
		}
		s.Add(Sample{Metric: "machine_cores", Labels: Labels{"job": "api"}, T: ts, V: 8})
		s.Add(Sample{Metric: "machine_cores", Labels: Labels{"job": "web"}, T: ts, V: 4})
	}
	return s
}

func copyLabels(l Labels) Labels {
	out := Labels{}
	for k, v := range l {
		out[k] = v
	}
	return out
}
