package rfc9002

import "time"

// RFC 9002 Section A.2 "Constants of Interest" and related RECOMMENDED
// values. The lowercase RFC-style names are provided for fidelity with the
// pseudocode; exported Go-style aliases are provided for external callers.
const (
	// kPacketThreshold is the RECOMMENDED packet reordering threshold.
	kPacketThreshold PacketNumber = 3
	// KPacketThreshold is the exported alias of kPacketThreshold.
	KPacketThreshold = 3
	// PacketThresholdDefault is the default packet threshold.
	PacketThresholdDefault PacketNumber = 3

	// kTimeThreshold is the time reordering threshold multiplier.
	kTimeThresholdNumerator   = 9
	kTimeThresholdDenominator = 8

	// kGranularity is the RECOMMENDED timer granularity.
	kGranularity       = time.Millisecond
	KGranularity       = time.Millisecond
	DefaultGranularity = time.Millisecond

	// kInitialRtt is the RECOMMENDED initial RTT before any sample exists.
	kInitialRtt       = 333 * time.Millisecond
	KInitialRtt       = 333 * time.Millisecond
	KInitialRTT       = 333 * time.Millisecond
	DefaultInitialRTT = 333 * time.Millisecond

	// kPersistentCongestionThreshold is the RECOMMENDED number of PTO
	// periods that define persistent congestion.
	kPersistentCongestionThreshold       = 3
	KPersistentCongestionThreshold       = 3
	DefaultPersistentCongestionThreshold = 3

	// AntiAmplificationFactor is RFC 9000's server anti-amplification limit.
	AntiAmplificationFactorDefault = 3

	// DefaultMaxDatagramSize is the QUIC minimum maximum datagram size.
	DefaultMaxDatagramSize = 1200

	// DefaultMaxAckDelay is the RFC 9000 default max_ack_delay transport
	// parameter (25ms).
	DefaultMaxAckDelay = 25 * time.Millisecond

	// DefaultLossReductionFactor is the NewReno / RFC 9002 Appendix B
	// multiplicative decrease factor.
	DefaultLossReductionFactor = 0.5

	// InfiniteWindow represents an unbounded slow-start threshold.
	InfiniteWindow = int(^uint(0) >> 1)
)

// Config tunes a LossDetector. The zero value is valid and is filled with
// RFC RECOMMENDED defaults lazily.
type Config struct {
	// MaxAckDelay is the peer max_ack_delay transport parameter. It is only
	// applied to Application Data RTT samples after handshake confirmation
	// and to the Application Data PTO.
	MaxAckDelay time.Duration

	// PeerMaxAckDelay is an alternative name for MaxAckDelay. When non-zero
	// it takes precedence, helping callers that model the peer transport
	// parameter explicitly.
	PeerMaxAckDelay time.Duration

	// Granularity is kGranularity, the minimum timer/timeout granularity.
	Granularity time.Duration

	// InitialRTT is kInitialRtt, used by PTO before the first RTT sample.
	InitialRTT time.Duration

	// PacketThreshold is kPacketThreshold. Zero defaults to 3.
	PacketThreshold PacketNumber

	// TimeThreshold is kTimeThreshold. Zero defaults to 9/8.
	TimeThreshold float64

	// PersistentCongestionThreshold is kPersistentCongestionThreshold.
	PersistentCongestionThreshold int

	// LossReductionFactor is the multiplicative decrease factor. Zero
	// defaults to 0.5.
	LossReductionFactor float64

	// MaxDatagramSize is used to derive the initial/minimum windows.
	MaxDatagramSize int

	// InitialWindow seeds CongestionWindow. Zero derives the RFC 9002
	// initial window from MaxDatagramSize.
	InitialWindow int

	// MinimumWindow is the floor used after loss halving and the window
	// used after persistent congestion. Zero defaults to 2*MaxDatagramSize.
	MinimumWindow int

	// CongestionWindow optionally seeds the live congestion window.
	CongestionWindow int

	// SlowStartThreshold optionally seeds ssthresh. Zero means infinite.
	SlowStartThreshold int

	// AntiAmplificationFactor is the RFC 9000 amplification multiplier.
	// Zero defaults to 3.
	AntiAmplificationFactor int

	// PTOProbePackets is how many ack-eliciting probes a PTO expiry asks
	// the transport to send (RFC allows one or two). Zero defaults to 2.
	PTOProbePackets int

	// IsServer marks the endpoint as a server for anti-amplification.
	IsServer bool

	// HandshakeConfirmed gates max_ack_delay subtraction and Application
	// Data PTO.
	HandshakeConfirmed bool

	// AddressValidated marks the peer address as validated. For a server
	// this clears anti-amplification limiting.
	AddressValidated bool

	// EnforceAmplification applies the anti-amplification timer gate even
	// when IsServer is false. Useful for focused state-machine tests.
	EnforceAmplification bool

	// DisableAutomaticPersistentCongestion turns off the RFC 9002 7.6.2
	// time-span persistent-congestion test inside DetectLostPackets. When
	// true, loss always takes the normal halving path; callers can still
	// trigger the minimum-window reset explicitly via
	// OnPersistentCongestion. Automatic detection is enabled by default.
	DisableAutomaticPersistentCongestion bool
}

// DefaultConfig returns the RFC RECOMMENDED configuration.
func DefaultConfig() Config {
	c := Config{
		MaxAckDelay:                   DefaultMaxAckDelay,
		Granularity:                   DefaultGranularity,
		InitialRTT:                    DefaultInitialRTT,
		PacketThreshold:               PacketThresholdDefault,
		TimeThreshold:                 9.0 / 8.0,
		PersistentCongestionThreshold: DefaultPersistentCongestionThreshold,
		LossReductionFactor:           DefaultLossReductionFactor,
		MaxDatagramSize:               DefaultMaxDatagramSize,
		AntiAmplificationFactor:       AntiAmplificationFactorDefault,
		PTOProbePackets:               2,
		SlowStartThreshold:            InfiniteWindow,
	}
	mss := c.MaxDatagramSize
	c.MinimumWindow = 2 * mss
	// RFC 9002 Appendix B.2 / RFC 6928 initial window.
	clamped10 := 10 * mss
	rfcFloor := 2 * mss
	if 14720 > rfcFloor {
		rfcFloor = 14720
	}
	if clamped10 < rfcFloor {
		c.InitialWindow = clamped10
	} else {
		c.InitialWindow = rfcFloor
	}
	c.CongestionWindow = c.InitialWindow
	return c
}

func (c *Config) ensureDefaults() {
	d := DefaultConfig()
	if c.MaxAckDelay == 0 {
		c.MaxAckDelay = d.MaxAckDelay
	}
	if c.Granularity <= 0 {
		c.Granularity = d.Granularity
	}
	if c.InitialRTT <= 0 {
		c.InitialRTT = d.InitialRTT
	}
	if c.PacketThreshold == 0 {
		c.PacketThreshold = d.PacketThreshold
	}
	if c.TimeThreshold == 0 {
		c.TimeThreshold = d.TimeThreshold
	}
	if c.PersistentCongestionThreshold == 0 {
		c.PersistentCongestionThreshold = d.PersistentCongestionThreshold
	}
	if c.LossReductionFactor == 0 {
		c.LossReductionFactor = d.LossReductionFactor
	}
	if c.MaxDatagramSize == 0 {
		c.MaxDatagramSize = d.MaxDatagramSize
	}
	if c.AntiAmplificationFactor == 0 {
		c.AntiAmplificationFactor = d.AntiAmplificationFactor
	}
	if c.PTOProbePackets == 0 {
		c.PTOProbePackets = d.PTOProbePackets
	}
	if c.MinimumWindow == 0 {
		c.MinimumWindow = 2 * c.MaxDatagramSize
	}
	if c.InitialWindow == 0 {
		c.InitialWindow = d.InitialWindow
	}
	if c.CongestionWindow == 0 {
		c.CongestionWindow = c.InitialWindow
	}
	if c.SlowStartThreshold == 0 {
		c.SlowStartThreshold = InfiniteWindow
	}
}

// effectiveMaxAckDelay resolves the peer max_ack_delay from either field.
func (c *Config) effectiveMaxAckDelay() time.Duration {
	if c.PeerMaxAckDelay > 0 {
		return c.PeerMaxAckDelay
	}
	return c.MaxAckDelay
}
