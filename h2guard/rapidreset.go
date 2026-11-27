package h2guard

import "time"

// Rapid reset pattern (CVE-2023-44487): a client opens a stream with
// HEADERS and immediately tears it down with RST_STREAM before the server
// finishes, so the request never produces a response while still burning
// server CPU/memory. This detector keeps a sliding time window of stream
// openings and client-initiated resets and trips when both an absolute count
// and a reset ratio threshold are exceeded.

// rrEvent marks one stream lifecycle event in the window.
type rrEvent struct {
	at    time.Time
	reset bool // true = client RST_STREAM before completion, false = open
}

// RapidResetDetector is a goroutine-unsafe sliding window counter. The parser
// owns one instance and serializes access through its Handle method.
type RapidResetDetector struct {
	window      time.Duration
	maxResets   int
	minRatioNum int // ratio numerator
	minRatioDen int // ratio denominator
	minSamples  int // minimum openings before the ratio can trip
	events      []rrEvent
	opens       int
	resets      int
	now         func() time.Time
	tripped     bool
}

// RapidResetConfig configures the detector. Zero values fall back to the
// defaults in DefaultConfig.
type RapidResetConfig struct {
	// Window is the sliding window length.
	Window time.Duration
	// MaxResets is the hard count of early client resets inside Window that
	// contributes to tripping.
	MaxResets int
	// ResetRatio trips when resets/opens >= ResetRatio (0..1).
	ResetRatio float64
	// MinOpens prevents the ratio from tripping on tiny samples.
	MinOpens int
}

func newRapidResetDetector(cfg RapidResetConfig, now func() time.Time) *RapidResetDetector {
	if now == nil {
		now = time.Now
	}
	num, den := ratioToFraction(cfg.ResetRatio)
	return &RapidResetDetector{
		window:      cfg.Window,
		maxResets:   cfg.MaxResets,
		minRatioNum: num,
		minRatioDen: den,
		minSamples:  cfg.MinOpens,
		now:         now,
	}
}

// ratioToFraction turns e.g. 0.5 into (1,2) using a bounded continued
// fraction expansion; the detector only needs a stable comparison.
func ratioToFraction(r float64) (num, den int) {
	if r <= 0 {
		return 0, 1
	}
	if r >= 1 {
		return 1, 1
	}
	const maxDen = 1000
	num0, den0, num1, den1 := 0, 1, 1, 0
	x := r
	for den1 < maxDen {
		a := int(x)
		nextNum := a*num1 + num0
		nextDen := a*den1 + den0
		if nextDen > maxDen {
			break
		}
		num0, den0 = num1, den1
		num1, den1 = nextNum, nextDen
		rest := x - float64(a)
		if rest < 1e-12 {
			break
		}
		x = 1 / rest
	}
	return num1, den1
}

// prune drops events older than the window relative to now.
func (d *RapidResetDetector) prune(now time.Time) {
	cutoff := now.Add(-d.window)
	drop := 0
	for drop < len(d.events) && d.events[drop].at.Before(cutoff) {
		if d.events[drop].reset {
			d.resets--
		} else {
			d.opens--
		}
		drop++
	}
	if drop > 0 {
		d.events = d.events[drop:]
	}
}

// RecordOpen records a client stream opening (a HEADERS frame starting a new
// stream). It returns true if the detector has tripped.
func (d *RapidResetDetector) RecordOpen(at time.Time) bool {
	if d.tripped {
		return true
	}
	d.prune(at)
	d.events = append(d.events, rrEvent{at: at})
	d.opens++
	return d.checkTripped()
}

// RecordEarlyReset records a client RST_STREAM on a stream that had been
// opened but not completed. It returns true if the detector has tripped.
func (d *RapidResetDetector) RecordEarlyReset(at time.Time) bool {
	if d.tripped {
		return true
	}
	d.prune(at)
	d.events = append(d.events, rrEvent{at: at, reset: true})
	d.resets++
	return d.checkTripped()
}

func (d *RapidResetDetector) checkTripped() bool {
	if d.tripped {
		return true
	}
	if d.resets < d.maxResets {
		return false
	}
	if d.opens < d.minSamples {
		return false
	}
	// resets/(opens+resets) >= num/den
	//   <=> resets*den >= (opens+resets)*num
	if d.resets*d.minRatioDen < (d.opens+d.resets)*d.minRatioNum {
		return false
	}
	d.tripped = true
	return true
}

// Tripped reports whether the detector has already tripped.
func (d *RapidResetDetector) Tripped() bool { return d.tripped }

// Stats is a point-in-time snapshot for logging/metrics.
type Stats struct {
	Opens  int
	Resets int
}

// Snapshot returns the current in-window counters after pruning.
func (d *RapidResetDetector) Snapshot() Stats {
	d.prune(d.now())
	return Stats{Opens: d.opens, Resets: d.resets}
}
