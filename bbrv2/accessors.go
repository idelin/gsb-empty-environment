package bbrv2

import "time"

// State returns the current top-level state.
func (c *Controller) State() State { return c.state }

// ProbePhase returns the current PROBE_BW sub-phase.
func (c *Controller) ProbePhase() ProbePhase { return c.probePhase }

// BtlBw returns the windowed-max bottleneck bandwidth estimate in bytes/sec.
func (c *Controller) BtlBw() uint64 { return c.btlBw }

// MinRTT returns the minimum propagation delay estimate.
func (c *Controller) MinRTT() time.Duration { return c.minRTT }

// Inflight returns the current bytes in flight.
func (c *Controller) Inflight() uint64 { return c.inflight }

// Cwnd returns the current congestion window in bytes.
func (c *Controller) Cwnd() uint64 { return c.cwnd }

// PacingRate returns the smoothed pacing rate in bytes/sec.
func (c *Controller) PacingRate() uint64 { return c.pacingRate }

// PacingRateTarget returns the unsmoothed target pacing rate in bytes/sec.
func (c *Controller) PacingRateTarget() uint64 { return c.pacingRateTarget }

// InflightHi returns the current safe in-flight ceiling in bytes.
func (c *Controller) InflightHi() uint64 { return c.inflightHi }

// HeadroomTarget returns HeadroomFactor * inflight_hi, the DOWN/CRUISE ceiling.
func (c *Controller) HeadroomTarget() uint64 { return c.headroomTarget() }

// BDP returns the current bandwidth-delay product in bytes.
func (c *Controller) BDP() uint64 { return c.bdp() }

// RoundCount returns the number of completed Round-Trip rounds.
func (c *Controller) RoundCount() uint32 { return c.roundCount }

// FullPipe reports whether STARTUP has declared the pipe full.
func (c *Controller) FullPipe() bool { return c.fullPipe }

// ProbeRTTDwell reports the time spent below the PROBE_RTT target; it is zero
// until real in-flight bytes have dropped below the target window.
func (c *Controller) ProbeRTTDwell() time.Duration {
	if c.probeRTTStart.IsZero() {
		return 0
	}
	return c.now.Sub(c.probeRTTStart)
}

// MinRTTAge reports how long the current minRTT estimate has been valid.
func (c *Controller) MinRTTAge() time.Duration {
	if !c.minRTTSet {
		return 0
	}
	return c.now.Sub(c.minRTTTime)
}

// ProbeInflight returns the exponential UP probe allowance in bytes.
func (c *Controller) ProbeInflight() uint64 { return c.probeInflight }
