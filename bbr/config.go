// Package bbr implements the BBRv2 congestion control state machine described
// in the BBRv2 Internet-Draft: a four-phase main state machine (STARTUP,
// DRAIN, PROBE_BW, PROBE_RTT), the four-sub-phase ProbeBW cycle
// (DOWN -> CRUISE -> REFILL -> UP), explicit-loss/ECN driven dual bounds
// (bw_lo/bw_hi, inflight_lo/inflight_hi) and an app-limited-aware delivery
// rate sampler.
//
// All time values are int64 nanoseconds supplied by the caller (a virtual
// clock in deterministic simulations, time.Now().UnixNano() in real
// transports), so the state machine itself contains no wall-clock reads and
// is fully deterministic.
package bbr

import "time"

// State is the BBRv2 main state.
type State uint8

const (
	Startup  State = iota // 2.89x startup gain
	Drain                 // 1/2.89x gain, drain the startup queue down to BDP
	ProbeBW               // steady-state bandwidth probing cycle
	ProbeRTT              // periodic min-RTT probe
)

func (s State) String() string {
	switch s {
	case Startup:
		return "STARTUP"
	case Drain:
		return "DRAIN"
	case ProbeBW:
		return "PROBE_BW"
	case ProbeRTT:
		return "PROBE_RTT"
	default:
		return "UNKNOWN"
	}
}

// ProbePhase is one of the four ProbeBW sub-phases.
type ProbePhase uint8

const (
	ProbeDown   ProbePhase = iota // 0.9x gain, drain excess queue
	ProbeCruise                   // 1.0x gain, cruise under inflight_hi headroom cap
	ProbeRefill                   // 1.0x gain for 1 RTT, refill the pipe
	ProbeUp                       // 1.25x gain, exponential probe of inflight_hi
)

func (p ProbePhase) String() string {
	switch p {
	case ProbeDown:
		return "DOWN"
	case ProbeCruise:
		return "CRUISE"
	case ProbeRefill:
		return "REFILL"
	case ProbeUp:
		return "UP"
	default:
		return "UNKNOWN"
	}
}

// Config holds the tunable BBRv2 parameters. DefaultConfig returns the values
// from the BBRv2 draft; tests may override individual fields.
type Config struct {
	MSS uint32

	// InitialCwnd is the initial congestion window in packets.
	InitialCwnd int
	// MinCwnd is the minimum congestion window in packets (draft: 4).
	MinCwnd int

	// StartupGain is the STARTUP pacing/cwnd gain (2/ln2 ~= 2.89).
	StartupGain float64
	// DrainGain is the reciprocal of the startup gain.
	DrainGain float64

	// ProbeBW pacing gains of the four sub-phases.
	DownGain   float64 // 0.9
	CruiseGain float64 // 1.0
	RefillGain float64 // 1.0
	UpGain     float64 // 1.25

	// StartupFullGrowth is the bandwidth growth threshold per round (0.25).
	StartupFullGrowth float64
	// StartupFullRounds is the number of non-growing rounds that exits STARTUP.
	StartupFullRounds int

	// Beta is the multiplicative back-off factor (0.85).
	Beta float64
	// Headroom is the fraction of inflight_hi left un-utilized (0.15).
	Headroom float64

	// LossThresh is the explicit loss ratio threshold (2%).
	LossThresh float64
	// MinLossEvents is the minimum number of losses in a round needed to
	// declare congestion (filters tiny-round noise).
	MinLossEvents int
	// ECNThresh is the ECN-marked-ACK fraction threshold (50%).
	ECNThresh float64

	// ProbeRTTInterval is the min_rtt staleness threshold (10s).
	ProbeRTTInterval time.Duration
	// ProbeRTTMinDuration is the minimum PROBE_RTT dwell time (200ms).
	ProbeRTTMinDuration time.Duration
	// ProbeRTTCwndFraction caps the window at this fraction of BDP (0.5).
	ProbeRTTCwndFraction float64

	// CruiseDuration bounds the deterministic CRUISE dwell. Real BBRv2 uses a
	// randomized probe interval; the simulator uses a deterministic value.
	CruiseDuration time.Duration
	// MaxBwFilterWindow is the max-bw window-filter length (draft: ~10 RTTs,
	// the reference code uses ~10 probe cycles).
	MaxBwFilterWindow int

	// OnStateChange / OnPhaseChange are optional observation hooks used by the
	// simulator and tests. They fire after the transition completes.
	OnStateChange func(old, new State)
	OnPhaseChange func(old, new ProbePhase)
}

// DefaultConfig returns the parameter set recommended by the BBRv2 draft.
func DefaultConfig() *Config {
	return &Config{
		MSS:                  1448,
		InitialCwnd:          16,
		MinCwnd:              4,
		StartupGain:          2.8853900817779268, // 2/ln(2)
		DrainGain:            1.0 / 2.8853900817779268,
		DownGain:             0.9,
		CruiseGain:           1.0,
		RefillGain:           1.0,
		UpGain:               1.25,
		StartupFullGrowth:    0.25,
		StartupFullRounds:    3,
		Beta:                 0.85,
		Headroom:             0.15,
		LossThresh:           0.02,
		MinLossEvents:        2,
		ECNThresh:            0.50,
		ProbeRTTInterval:     10 * time.Second,
		ProbeRTTMinDuration:  200 * time.Millisecond,
		ProbeRTTCwndFraction: 0.5,
		CruiseDuration:       2 * time.Second,
		MaxBwFilterWindow:    10,
	}
}
