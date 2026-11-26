package rfc9002

import "time"

// Canonical short constant aliases matching the RFC pseudocode vocabulary.
const (
	// PacketThreshold is kPacketThreshold (3).
	PacketThreshold = 3
	// TimeThreshold is kTimeThreshold (9/8).
	TimeThreshold = 9.0 / 8.0
	// PersistentCongestionThreshold is kPersistentCongestionThreshold (3).
	PersistentCongestionThreshold = 3
	// AntiAmplificationFactor is the RFC 9000 multiplier (3).
	AntiAmplificationFactor = 3
)

// Type aliases under the names most commonly used for this component. They
// share LossDetector's method set.
type (
	// Recovery is an alias for LossDetector.
	Recovery = LossDetector
	// RecoveryState is an alias for LossDetector.
	RecoveryState = LossDetector
	// LossDetectionState is an alias for LossDetector.
	LossDetectionState = LossDetector
	// LossDetectorState is an alias for LossDetector.
	LossDetectorState = LossDetector
	// StateMachine is an alias for LossDetector.
	StateMachine = LossDetector
	// Manager is an alias for LossDetector.
	Manager = LossDetector
)

// NewAt constructs a detector and records an initial clock value. The state
// machine takes explicit `now` on every call, so start is currently advisory
// and mainly aids callers that want a named construction point.
func NewAt(_ time.Time, opts ...Option) *LossDetector {
	return NewLossDetector(opts...)
}

// NewRecoveryState is an alias for NewLossDetector.
func NewRecoveryState(opts ...Option) *LossDetector { return NewLossDetector(opts...) }

// NewLossDetectionState is an alias for NewLossDetector.
func NewLossDetectionState(opts ...Option) *LossDetector { return NewLossDetector(opts...) }

// HandleTimeout is an alias for OnLossDetectionTimeout.
func (ld *LossDetector) HandleTimeout(now time.Time) *TimeoutOutcome {
	return ld.OnLossDetectionTimeout(now)
}

// OnTimer is an alias for OnLossDetectionTimeout.
func (ld *LossDetector) OnTimer(now time.Time) *TimeoutOutcome {
	return ld.OnLossDetectionTimeout(now)
}

// Timer returns the last resolved unified timer state, recomputing it at now.
func (ld *LossDetector) TimerAt(now time.Time) TimerState {
	return ld.SetLossDetectionTimer(now)
}

// GetPtoCount returns pto_count.
func (ld *LossDetector) GetPtoCount() int { ld.ensure(); return ld.PTOCount }

// GetBytesInFlight returns bytes_in_flight.
func (ld *LossDetector) GetBytesInFlight() int { ld.ensure(); return ld.BytesInFlight }

// GetCongestionWindow returns the live congestion window.
func (ld *LossDetector) GetCongestionWindow() int { ld.ensure(); return ld.CongestionWindow }

// GetMinRTT returns min_rtt.
func (ld *LossDetector) GetMinRTT() time.Duration { ld.ensure(); return ld.MinRTT }

// GetSmoothedRTT returns smoothed_rtt.
func (ld *LossDetector) GetSmoothedRTT() time.Duration { ld.ensure(); return ld.SmoothedRTT }

// GetRTTVar returns rttvar.
func (ld *LossDetector) GetRTTVar() time.Duration { ld.ensure(); return ld.RTTVar }

// GetLatestRTT returns latest_rtt.
func (ld *LossDetector) GetLatestRTT() time.Duration { ld.ensure(); return ld.LatestRTT }

// Package-level RFC-style function forms for callers/tests that expect the
// Appendix A pseudocode functions as free functions operating on the state.

// GetLossTimeAndSpace (free-function form) returns the earliest loss deadline
// across spaces. It coexists with the same-named method.
func GetLossTimeAndSpace(ld *LossDetector) (time.Time, PacketNumberSpace) {
	return ld.GetLossTimeAndSpace()
}

// GetPtoTimeAndSpace (free-function form) returns the earliest PTO deadline.
// It coexists with the same-named method.
func GetPtoTimeAndSpace(ld *LossDetector) (time.Time, PacketNumberSpace) {
	return ld.GetPtoTimeAndSpace()
}
