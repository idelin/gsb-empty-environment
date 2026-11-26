// Package rfc9002 is an importable, deterministic Go implementation of the
// QUIC loss-detection and congestion-event state machine defined by RFC 9002
// (with RFC 9000 anti-amplification handling).
//
// It maintains the three independent packet-number spaces (Initial,
// Handshake, ApplicationData), per-space sent-packet tables and
// largest_acked, the four RTT observables (LatestRTT, MinRTT, SmoothedRTT,
// RTTVar), the packet threshold kPacketThreshold = 3 and the time threshold
// max(9/8*max(latest_rtt, smoothed_rtt), kGranularity).
//
// All wall-clock inputs are explicit time.Time arguments, so the machine is
// deterministic under a fake clock. GetLossTimeAndSpace and
// GetPtoTimeAndSpace feed a single unified timer resolved by
// SetLossDetectionTimer / NextDeadline and advanced by
// OnLossDetectionTimeout.
//
// The zero value is usable after lazy initialization, but NewLossDetector
// applies the RFC RECOMMENDED defaults.
package rfc9002
