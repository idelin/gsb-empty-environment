// Package bbrv2 implements a BBRv2 style model-based congestion controller.
//
// The controller jointly estimates the bottleneck bandwidth (BtlBw) with a
// Kathleen Nichols three-sample windowed max filter, the minimum propagation
// delay (minRTT) and a safe in-flight ceiling (inflight_hi), and drives a
// four-state machine (STARTUP, DRAIN, PROBE_BW, PROBE_RTT) plus the four
// PROBE_BW sub-phases (DOWN, CRUISE, REFILL, UP).
//
// All quantities are expressed in bytes and time.Duration; the package has no
// dependency beyond the Go standard library and is safe for single-goroutine
// use from a transport's ACK processing path.
package bbrv2

import (
	"math"
	"time"
)

// State is one of the four top-level BBRv2 states.
type State uint8

const (
	StateStartup State = iota
	StateDrain
	StateProbeBW
	StateProbeRTT
)

func (s State) String() string {
	switch s {
	case StateStartup:
		return "STARTUP"
	case StateDrain:
		return "DRAIN"
	case StateProbeBW:
		return "PROBE_BW"
	case StateProbeRTT:
		return "PROBE_RTT"
	default:
		return "UNKNOWN"
	}
}

// ProbePhase is one of the four PROBE_BW sub-phases.
type ProbePhase uint8

const (
	PhaseDown ProbePhase = iota
	PhaseCruise
	PhaseRefill
	PhaseUp
)

func (p ProbePhase) String() string {
	switch p {
	case PhaseDown:
		return "DOWN"
	case PhaseCruise:
		return "CRUISE"
	case PhaseRefill:
		return "REFILL"
	case PhaseUp:
		return "UP"
	default:
		return "UNKNOWN"
	}
}

// Default tuning constants. Values mirror the BBRv2 draft / Linux BBR family.
const (
	defaultMSS                     uint64        = 1460
	defaultInitialCwndPackets      int           = 10
	defaultBwFilterWindow          uint32        = 10 // rounds, BBR_BW_WIN in round units
	defaultStartupGrowthFactor     float64       = 1.25
	defaultFullBwRounds            int           = 3
	defaultStartupPacingGain       float64       = 2.885 // 2/ln(2)
	defaultDrainPacingGain         float64       = 1.0 / defaultStartupPacingGain
	defaultProbeCwndGain           float64       = 2.0
	defaultDownCruisePacingGain    float64       = 0.9
	defaultRefillPacingGain        float64       = 1.0
	defaultUpPacingGain            float64       = defaultStartupPacingGain
	defaultHeadroomFactor          float64       = 0.85
	defaultLossThreshold           float64       = 0.02
	defaultECNThreshold            float64       = 0.5
	defaultMinRTTExpiry            time.Duration = 10 * time.Second
	defaultProbeRTTMinDuration     time.Duration = 200 * time.Millisecond
	defaultProbeRTTCwndFactor      float64       = 0.5
	defaultMinCwndPackets          int           = 4
	defaultCruiseMaxDuration       time.Duration = 2 * time.Second
	defaultMaxProbeInflightPackets int           = 32
	defaultPacingSmoothingAlpha    float64       = 0.5 // EMA weight toward target rate
	nanosPerSecond                 uint64        = 1_000_000_000
)

// Config parameterises a Controller. Zero fields are replaced by defaults.
type Config struct {
	MSS                     uint64
	InitialCwndPackets      int
	InitialBandwidth        uint64 // bytes/sec; 0 derives a conservative default
	BwFilterWindowRounds    uint32
	StartupGrowthFactor     float64 // BtlBw growth required per round to stay in STARTUP
	FullBwRounds            int     // consecutive low-growth rounds that declare a full pipe
	StartupPacingGain       float64
	DrainPacingGain         float64
	ProbeCwndGain           float64
	DownCruisePacingGain    float64
	RefillPacingGain        float64
	UpPacingGain            float64
	HeadroomFactor          float64       // cruise/down target = HeadroomFactor * inflight_hi
	LossThreshold           float64       // per-round loss rate that triggers inflight_hi shrink
	ECNThreshold            float64       // per-round ECN mark ratio that triggers inflight_hi shrink
	MinRTTExpiry            time.Duration // stale minRTT age that triggers PROBE_RTT
	ProbeRTTMinDuration     time.Duration // floor for the PROBE_RTT dwell
	ProbeRTTCwndFactor      float64       // BDP fraction for the PROBE_RTT cwnd
	MinCwndPackets          int
	CruiseMaxDuration       time.Duration // time spent cruising before refilling
	MaxProbeInflightPackets int           // cap on exponential UP probing
	PacingSmoothingAlpha    float64
	PacingMaxGrowthFactor   float64 // 0 (default) disables the upward step cap
}

// RateSample is one delivery-rate sample produced by the transport ACK path.
type RateSample struct {
	Now            time.Time
	RTT            time.Duration
	BytesDelivered uint64        // bytes newly delivered (ACKed or SACKed) during Interval
	BytesAcked     uint64        // bytes leaving in-flight because they were acknowledged
	BytesLost      uint64        // bytes leaving in-flight because they were declared lost
	BytesECN       uint64        // delivered bytes bearing an ECN mark
	Interval       time.Duration // delivery interval for BytesDelivered
	AppLimited     bool          // force the sample to be treated as application-limited
}

// Controller is the BBRv2 state machine and rate/window controller.
type Controller struct {
	cfg Config

	// Model estimates.
	bwFilter   *maxFilter
	btlBw      uint64
	minRTT     time.Duration
	minRTTTime time.Time
	minRTTSet  bool

	// In-flight bookkeeping.
	inflight uint64
	cwnd     uint64

	// Round-Trip boundary bookkeeping.
	roundCount         uint32
	peakResetRound     uint32 // round for which roundMaxInflight was (re)initialised
	totalDelivered     uint64
	nextRoundDelivered uint64

	// Per-round aggregation.
	roundDelivered     uint64
	roundLost          uint64
	roundECN           uint64
	roundHasCongestion bool
	roundAppLimited    bool
	roundHasBw         bool
	roundPeakBw        uint64 // highest raw delivery rate observed this round
	congestionInflight uint64 // in-flight bytes observed at the latest sample
	roundMaxInflight   uint64 // peak in-flight bytes across the current round

	// app_limited flag (set by the application layer, consumed per sample).
	appLimited bool

	// STARTUP full-pipe detection.
	fullBw    uint64
	fullBwCnt uint32
	fullPipe  bool

	// PROBE_BW bookkeeping.
	inflightHi      uint64
	probeInflight   uint64
	phaseEnterTime  time.Time
	phaseEnterRound uint32
	upStartBw       uint64
	upNoGrowthCnt   uint32

	// PROBE_RTT bookkeeping.
	probeRTTStart time.Time // zero until in-flight drops below the target cwnd

	// Pacing output.
	pacingRate       uint64 // smoothed output
	pacingRateTarget uint64 // unsmoothed target derived from BtlBw * gain

	state      State
	probePhase ProbePhase
	now        time.Time
}

// New constructs a Controller in STARTUP with the given configuration.
func New(cfg Config) *Controller {
	cfg = cfg.withDefaults()
	minCwnd := uint64(cfg.MinCwndPackets) * cfg.MSS
	initialCwnd := uint64(cfg.InitialCwndPackets) * cfg.MSS
	if initialCwnd < minCwnd {
		initialCwnd = minCwnd
	}
	c := &Controller{
		cfg:             cfg,
		bwFilter:        newMaxFilter(cfg.BwFilterWindowRounds),
		cwnd:            initialCwnd,
		roundAppLimited: true,
		state:           StateStartup,
		probePhase:      PhaseDown,
	}
	// A conservative initial pacing rate: drain the initial window per second.
	initRate := cfg.InitialBandwidth
	if initRate == 0 {
		initRate = initialCwnd
	}
	c.pacingRate = scaleFloat(initRate, cfg.StartupPacingGain)
	c.pacingRateTarget = c.pacingRate
	return c
}

func (c *Config) withDefaults() Config {
	d := *c
	setU64 := func(dst *uint64, v uint64) {
		if *dst == 0 {
			*dst = v
		}
	}
	setI := func(dst *int, v int) {
		if *dst == 0 {
			*dst = v
		}
	}
	setU32 := func(dst *uint32, v uint32) {
		if *dst == 0 {
			*dst = v
		}
	}
	setF := func(dst *float64, v float64) {
		if *dst == 0 {
			*dst = v
		}
	}
	setDur := func(dst *time.Duration, v time.Duration) {
		if *dst == 0 {
			*dst = v
		}
	}
	setU64(&d.MSS, defaultMSS)
	setI(&d.InitialCwndPackets, defaultInitialCwndPackets)
	setU32(&d.BwFilterWindowRounds, defaultBwFilterWindow)
	setF(&d.StartupGrowthFactor, defaultStartupGrowthFactor)
	setI(&d.FullBwRounds, defaultFullBwRounds)
	setF(&d.StartupPacingGain, defaultStartupPacingGain)
	setF(&d.DrainPacingGain, defaultDrainPacingGain)
	setF(&d.ProbeCwndGain, defaultProbeCwndGain)
	setF(&d.DownCruisePacingGain, defaultDownCruisePacingGain)
	setF(&d.RefillPacingGain, defaultRefillPacingGain)
	setF(&d.UpPacingGain, defaultUpPacingGain)
	setF(&d.HeadroomFactor, defaultHeadroomFactor)
	setF(&d.LossThreshold, defaultLossThreshold)
	setF(&d.ECNThreshold, defaultECNThreshold)
	setDur(&d.MinRTTExpiry, defaultMinRTTExpiry)
	setDur(&d.ProbeRTTMinDuration, defaultProbeRTTMinDuration)
	setF(&d.ProbeRTTCwndFactor, defaultProbeRTTCwndFactor)
	setI(&d.MinCwndPackets, defaultMinCwndPackets)
	setDur(&d.CruiseMaxDuration, defaultCruiseMaxDuration)
	setI(&d.MaxProbeInflightPackets, defaultMaxProbeInflightPackets)
	setF(&d.PacingSmoothingAlpha, defaultPacingSmoothingAlpha)
	return d
}

// OnSent accounts for bytes newly handed to the transport for transmission.
func (c *Controller) OnSent(bytes uint64) {
	c.inflight += bytes
}

// SetAppLimited marks the sender application-limited. The flag is consumed by
// delivery samples and automatically clears once the pipe is full again
// (in-flight reaches the current congestion window).
func (c *Controller) SetAppLimited() {
	c.appLimited = true
}

// AppLimited reports whether the application-limited flag is currently set.
func (c *Controller) AppLimited() bool {
	return c.appLimited
}

// OnSample ingests one delivery-rate sample and advances the state machine.
func (c *Controller) OnSample(s RateSample) {
	if !s.Now.IsZero() {
		c.now = s.Now
	}

	// Lazily (re)start the in-flight peak accumulator on the first sample
	// after a round boundary, preserving the frozen previous-round peak until
	// the new round actually observes in-flight data.
	if c.peakResetRound != c.roundCount {
		c.peakResetRound = c.roundCount
		c.roundMaxInflight = 0
	}

	// In-flight bytes leave the pipe on ACK or loss. Capture the pre-ACK
	// in-flight value first: inflight_hi adaptation must reason about the
	// amount of data that was actually in the pipe.
	c.congestionInflight = c.inflight
	if c.inflight > c.roundMaxInflight {
		c.roundMaxInflight = c.inflight
	}
	leaving := s.BytesAcked + s.BytesLost
	if leaving > c.inflight {
		leaving = c.inflight
	}
	c.inflight -= leaving

	sampleAppLimited := c.appLimited || s.AppLimited

	c.totalDelivered += s.BytesDelivered
	c.roundDelivered += s.BytesDelivered
	c.roundLost += s.BytesLost
	c.roundECN += s.BytesECN
	if s.BytesLost > 0 || s.BytesECN > 0 {
		c.roundHasCongestion = true
	}
	if sampleAppLimited {
		c.roundAppLimited = true
	}

	if s.RTT > 0 {
		c.updateMinRTT(s.RTT)
	}
	if s.Interval > 0 && s.BytesDelivered > 0 {
		bw := s.BytesDelivered * nanosPerSecond / uint64(s.Interval.Nanoseconds())
		// A round carrying any loss/ECN signal overstates the safe delivery
		// rate (queue draining), so none of its rate samples enter the
		// BtlBw max filter.
		if !c.roundHasCongestion {
			c.updateBandwidth(bw, sampleAppLimited)
		}
	}

	// A Round-Trip completes once all data outstanding at the previous
	// boundary has been delivered. handleRoundEnd consumes the aggregates and
	// peak in-flight level collected during the round that just finished.
	if c.totalDelivered >= c.nextRoundDelivered {
		c.handleRoundEnd()
		c.nextRoundDelivered = c.totalDelivered
		// The finished round's aggregates were consumed above; open the new
		// round before the next sample arrives.
		c.roundDelivered = 0
		c.roundLost = 0
		c.roundECN = 0
		c.roundHasCongestion = false
		c.roundAppLimited = false
		c.roundHasBw = false
		c.roundPeakBw = 0
	}

	// The application is no longer limited once it refills the pipe.
	if c.inflight >= c.cwnd {
		c.appLimited = false
	}

	c.updateState()
	c.updateCwnd()
	c.updatePacingRate()
}

// updateMinRTT refreshes the minimum propagation delay estimate. A sample
// equal to the current minimum also refreshes the timestamp so the 10 second
// expiry only fires when genuinely lower samples stop arriving.
func (c *Controller) updateMinRTT(rtt time.Duration) {
	if !c.minRTTSet || rtt <= c.minRTT {
		c.minRTT = rtt
		c.minRTTTime = c.now
		c.minRTTSet = true
	}
}

// updateBandwidth feeds the windowed max filter. Invariant: an
// application-limited sample may never lower the estimate or drive full-pipe
// detection, but an app-limited sample above the current maximum is still
// adopted because it proves the pipe can carry at least that rate.
func (c *Controller) updateBandwidth(bw uint64, appLimited bool) {
	c.roundHasBw = true
	if bw > c.roundPeakBw {
		c.roundPeakBw = bw
	}
	if !appLimited || bw >= c.btlBw {
		c.btlBw = c.bwFilter.runningMax(c.roundCount, bw)
	}
}

// handleRoundEnd runs the round-driven full-pipe detection, congestion
// response and PROBE_BW phase bookkeeping.
func (c *Controller) handleRoundEnd() {
	c.roundCount++
	congested := c.roundCongested()

	entryState := c.state
	switch c.state {
	case StateStartup:
		c.handleStartupRound(congested)
	case StateDrain:
		// DRAIN completes once the STARTUP queue is gone: in-flight at or
		// below the model BDP, evaluated at the round boundary.
		if c.inflight <= c.bdp() {
			c.enterProbeBW()
		}
	}
	// Run PROBE_BW phase bookkeeping only when the finished round was itself
	// a PROBE_BW round and no state transition happened at this boundary;
	// otherwise the new state starts clean at the next round.
	if entryState == StateProbeBW && c.state == entryState {
		c.handleProbeBWRound(congested)
	}
}

// roundCongested reports whether the just-finished round crossed either the
// loss or the ECN adaptation threshold. Rounds without delivered data cannot
// establish a statistically meaningful ratio and are treated as healthy.
func (c *Controller) roundCongested() bool {
	total := c.roundDelivered + c.roundLost
	if total == 0 {
		return false
	}
	lossRate := float64(c.roundLost) / float64(total)
	if lossRate > c.cfg.LossThreshold {
		return true
	}
	if c.roundDelivered > 0 {
		ecnRate := float64(c.roundECN) / float64(c.roundDelivered)
		if ecnRate > c.cfg.ECNThreshold {
			return true
		}
	}
	return false
}

// handleStartupRound implements STARTUP full-pipe detection. A full pipe is
// declared after FullBwRounds consecutive non-app-limited rounds whose peak
// delivery rate grows by less than StartupGrowthFactor, or immediately on a
// significant loss/ECN round.
func (c *Controller) handleStartupRound(congested bool) {
	if congested {
		c.enterDrain()
		return
	}
	// Application-limited rounds carry no information about pipe capacity:
	// they must neither count toward nor reset the low-growth streak.
	if c.roundAppLimited || !c.roundHasBw {
		return
	}
	if c.btlBw >= scaleFloat(c.fullBw, c.cfg.StartupGrowthFactor) {
		c.fullBw = c.btlBw
		c.fullBwCnt = 0
		return
	}
	if c.fullBw == 0 {
		c.fullBw = c.btlBw
		c.fullBwCnt = 0
		return
	}
	c.fullBwCnt++
	if int(c.fullBwCnt) >= c.cfg.FullBwRounds {
		c.fullPipe = true
		c.enterDrain()
	}
}

// handleProbeBWRound drives the DOWN/CRUISE/REFILL/UP cycle and the
// inflight_hi adaptation on congestion.
func (c *Controller) handleProbeBWRound(congested bool) {
	if congested {
		c.adaptInflightHi()
		c.enterDown()
		return
	}

	switch c.probePhase {
	case PhaseDown:
		// Once the pipe has drained to the headroom ceiling, begin cruising.
		if c.roundCount > c.phaseEnterRound && c.inflight <= c.headroomTarget() {
			c.enterCruise()
		}
	case PhaseRefill:
		// One full round of refill guarantees the pipe was reloaded before
		// the exponential probe restarts.
		c.enterUp()
	case PhaseUp:
		// A round that just transitioned REFILL->UP starts the probe cleanly
		// and must not evaluate growth in the same boundary.
		if c.phaseEnterRound == c.roundCount {
			return
		}
		// Unconstrained probing: double the extra in-flight allowance each
		// round, capped, while BtlBw keeps responding. Three flat rounds mean
		// the probe has reached the pipe ceiling.
		c.probeInflight *= 2
		maxProbe := uint64(c.cfg.MaxProbeInflightPackets) * c.cfg.MSS
		if c.probeInflight > maxProbe {
			c.probeInflight = maxProbe
		}
		// Growth is judged on this round's peak raw rate, not the windowed
		// BtlBw max (which retains older, higher samples and would hide a
		// genuine fresh increase during probing).
		peak := c.roundPeakBw
		if peak == 0 {
			peak = c.btlBw
		}
		if peak >= scaleFloat(c.upStartBw, c.cfg.StartupGrowthFactor) {
			c.upStartBw = peak
			c.upNoGrowthCnt = 0
		} else {
			c.upNoGrowthCnt++
			if int(c.upNoGrowthCnt) >= c.cfg.FullBwRounds {
				c.enterDown()
			}
		}
	}
}

// adaptInflightHi shrinks the safe in-flight ceiling to the minimum of the
// in-flight level that experienced congestion and the delivery-derived safe
// bound, never below one BDP.
func (c *Controller) adaptInflightHi() {
	bdp := c.bdp()
	// BBRv2 bounds the new ceiling above by the in-flight level that
	// experienced congestion and below by the BDP delivery floor.
	newHi := c.roundMaxInflight
	if newHi == 0 {
		newHi = c.congestionInflight
	}
	if bdp > newHi {
		newHi = bdp
	}
	c.inflightHi = newHi
}

func (c *Controller) enterDrain() {
	c.state = StateDrain
}

func (c *Controller) enterProbeBW() {
	c.state = StateProbeBW
	// inflight_hi is persistent across PROBE_BW entries: it is only tightened
	// by congestion adaptation and raised by successful UP probing. Seed it
	// once (e.g. after STARTUP or a PROBE_RTT dwell) with the current window.
	if c.inflightHi == 0 {
		c.inflightHi = c.cwnd
	}
	c.enterDown()
}

func (c *Controller) enterDown() {
	c.probePhase = PhaseDown
	c.phaseEnterTime = c.now
	c.phaseEnterRound = c.roundCount
}

func (c *Controller) enterCruise() {
	c.probePhase = PhaseCruise
	c.phaseEnterTime = c.now
	c.phaseEnterRound = c.roundCount
}

func (c *Controller) enterRefill() {
	c.probePhase = PhaseRefill
	c.phaseEnterTime = c.now
	c.phaseEnterRound = c.roundCount
}

func (c *Controller) enterUp() {
	c.probePhase = PhaseUp
	c.phaseEnterTime = c.now
	c.phaseEnterRound = c.roundCount
	c.probeInflight = c.cfg.MSS
	c.upStartBw = c.btlBw
	c.upNoGrowthCnt = 0
}

// updateState runs the per-sample (rather than per-round) transitions: DRAIN
// completion, PROBE_RTT entry/dwell, and the PROBE_BW phase cycle.
func (c *Controller) updateState() {
	switch c.state {
	case StateStartup:
		if c.probeRTTDue() {
			c.enterProbeRTT()
		}
	case StateDrain:
		// DRAIN completion is evaluated at round boundaries below.
		if c.probeRTTDue() {
			c.enterProbeRTT()
		}
	case StateProbeBW:
		if c.probeRTTDue() {
			c.enterProbeRTT()
			return
		}
		c.updateProbePhase()
	case StateProbeRTT:
		c.updateProbeRTT()
	}
}

// updateProbePhase handles the sample-level sub-phase cycle. Congestion-driven
// DOWN entry and round-driven REFILL/UP bookkeeping live in
// handleProbeBWRound.
func (c *Controller) updateProbePhase() {
	switch c.probePhase {
	case PhaseCruise:
		// The cruise dwell is a pure time condition, evaluated per sample so
		// clock-only advances (no delivered data) can end the cruise.
		if c.now.Sub(c.phaseEnterTime) >= c.cfg.CruiseMaxDuration {
			c.enterRefill()
		}
	}
}

// probeRTTDue reports whether minRTT has been stale for MinRTTExpiry.
func (c *Controller) probeRTTDue() bool {
	if !c.minRTTSet {
		return false
	}
	return c.now.Sub(c.minRTTTime) >= c.cfg.MinRTTExpiry
}

func (c *Controller) enterProbeRTT() {
	c.state = StateProbeRTT
	c.probeRTTStart = time.Time{}
}

// updateProbeRTT implements the two-stage dwell: the timer starts only after
// real in-flight bytes fall below the reduced target window, and the state is
// held for at least max(ProbeRTTMinDuration, one minRTT).
func (c *Controller) updateProbeRTT() {
	target := c.probeRTTTarget()
	if c.probeRTTStart.IsZero() {
		if c.inflight <= target {
			c.probeRTTStart = c.now
		}
		return
	}
	elapsed := c.now.Sub(c.probeRTTStart)
	minDwell := c.cfg.ProbeRTTMinDuration
	if c.minRTT > minDwell {
		minDwell = c.minRTT
	}
	if elapsed >= minDwell {
		// Restart the minRTT expiry window and return to bandwidth probing.
		c.minRTTTime = c.now
		c.enterProbeBW()
	}
}

// probeRTTTarget implements max(4*MSS, 0.5*BDP); the final window is the
// minimum of the current cwnd and this target.
func (c *Controller) probeRTTTarget() uint64 {
	minPipe := uint64(c.cfg.MinCwndPackets) * c.cfg.MSS
	halfBDP := scaleFloat(c.bdp(), c.cfg.ProbeRTTCwndFactor)
	target := halfBDP
	if minPipe > target {
		target = minPipe
	}
	if c.cwnd < target {
		target = c.cwnd
	}
	return target
}

// bdp is the bandwidth-delay product BtlBw * minRTT in bytes.
func (c *Controller) bdp() uint64 {
	if c.btlBw == 0 || !c.minRTTSet {
		return uint64(c.cfg.MinCwndPackets) * c.cfg.MSS
	}
	return c.btlBw * uint64(c.minRTT.Nanoseconds()) / nanosPerSecond
}

// headroomTarget is the DOWN/CRUISE cruise ceiling: HeadroomFactor*inflight_hi.
func (c *Controller) headroomTarget() uint64 {
	return scaleFloat(c.inflightHi, c.cfg.HeadroomFactor)
}

// targetInflight computes the state/phase dependent in-flight target used to
// derive the congestion window.
func (c *Controller) targetInflight() uint64 {
	bdp := c.bdp()
	switch c.state {
	case StateStartup, StateDrain:
		return scaleFloat(bdp, c.cfg.StartupPacingGain)
	case StateProbeRTT:
		return c.probeRTTTarget()
	case StateProbeBW:
		switch c.probePhase {
		case PhaseUp:
			return c.inflightHi + c.probeInflight
		case PhaseRefill:
			return c.bdp() + c.cfg.MSS
		default: // DOWN and CRUISE
			return c.headroomTarget()
		}
	}
	return bdp
}

// updateCwnd derives the congestion window from the in-flight target. STARTUP
// and DRAIN keep a monotonic window; PROBE_BW follows the phase target;
// PROBE_RTT is capped at the reduced target. Every window respects the
// 4*MSS floor.
func (c *Controller) updateCwnd() {
	minCwnd := uint64(c.cfg.MinCwndPackets) * c.cfg.MSS
	target := c.targetInflight()
	if target < minCwnd {
		target = minCwnd
	}

	switch c.state {
	case StateStartup, StateDrain:
		if target > c.cwnd {
			c.cwnd = target
		}
	case StateProbeRTT, StateProbeBW:
		c.cwnd = target
	}
	if c.cwnd < minCwnd {
		c.cwnd = minCwnd
	}
}

// pacingGain returns the pacing gain of the active state/phase.
func (c *Controller) pacingGain() float64 {
	switch c.state {
	case StateStartup:
		return c.cfg.StartupPacingGain
	case StateDrain:
		return c.cfg.DrainPacingGain
	case StateProbeRTT:
		return 1
	case StateProbeBW:
		switch c.probePhase {
		case PhaseDown, PhaseCruise:
			return c.cfg.DownCruisePacingGain
		case PhaseRefill:
			return c.cfg.RefillPacingGain
		case PhaseUp:
			return c.cfg.UpPacingGain
		}
	}
	return 1
}

// updatePacingRate computes the target rate and applies an exponentially
// weighted smoothing filter, allowing at most a bounded upward step.
func (c *Controller) updatePacingRate() {
	target := scaleFloat(c.btlBw, c.pacingGain())
	if target == 0 {
		return
	}
	c.pacingRateTarget = target

	alpha := c.cfg.PacingSmoothingAlpha
	smoothed := uint64(math.Round(float64(c.pacingRate)*(1-alpha) + float64(target)*alpha))
	if c.cfg.PacingMaxGrowthFactor > 0 && c.pacingRate > 0 {
		maxGrowth := scaleFloat(c.pacingRate, c.cfg.PacingMaxGrowthFactor)
		if smoothed > maxGrowth {
			smoothed = maxGrowth
		}
	}
	// BBR-family asymmetric smoothing: rate increases track the target
	// immediately (the model estimate is the trustworthy ceiling), while
	// decreases are filtered through the EMA to avoid rate oscillation.
	if target >= c.pacingRate {
		smoothed = target
	}
	if smoothed == 0 {
		smoothed = target
	}
	c.pacingRate = smoothed
}

func scaleFloat(v uint64, gain float64) uint64 {
	if v == 0 || gain == 0 {
		return 0
	}
	return uint64(math.Round(float64(v) * gain))
}
