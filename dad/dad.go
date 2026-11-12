package dad

import (
	"bytes"
	"crypto/rand"
	"io"
	"net/netip"
)

// State describes the lifecycle of a tentative address.
type State int

const (
	// StateTentative means DAD is in progress; the address must not be
	// used yet.
	StateTentative State = iota
	// StateValid means DAD completed without conflict.
	StateValid
	// StateFailed means the probe and retry budgets were exhausted by
	// conflicts; the address stays unavailable and no further probes are
	// emitted.
	StateFailed
)

// Verdict is the decision returned to the ND receive path.
type Verdict int

const (
	// VerdictIgnore means the message is irrelevant to this DAD run.
	VerdictIgnore Verdict = iota
	// VerdictLoopback means the message echoes one of our own probes
	// (matched by nonce) and must not be counted as a conflict.
	VerdictLoopback
	// VerdictConflict means the tentative address is in use.
	VerdictConflict
)

// Config tunes a DAD run. Zero values pick defaults.
type Config struct {
	// Probes is the number of NS probes per attempt
	// (DupAddrDetectTransmits). Defaults to 3.
	Probes int
	// MaxRetries is how many times the whole DAD run may restart after a
	// conflict before the address is declared failed. Defaults to 0.
	MaxRetries int
	// Rand supplies the nonce entropy. Defaults to crypto/rand.Reader.
	Rand io.Reader
}

func (c Config) withDefaults() Config {
	if c.Probes <= 0 {
		c.Probes = 3
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	return c
}

// DAD tracks one tentative address through enhanced duplicate address
// detection.
type DAD struct {
	cfg     Config
	addr    netip.Addr
	state   State
	sent    int
	retries int
	nonces  [][]byte
}

// New starts DAD for addr, which must be an IPv6 address.
func New(addr netip.Addr, cfg Config) *DAD {
	return &DAD{cfg: cfg.withDefaults(), addr: addr}
}

// State reports the current lifecycle state.
func (d *DAD) State() State { return d.state }

// ProbesExhausted reports whether every probe of the current attempt has
// been sent.
func (d *DAD) ProbesExhausted() bool { return d.sent >= d.cfg.Probes }

// NextProbe returns the next DAD probe to transmit. ok is false once the
// per-attempt probe budget is spent or the run reached a terminal state;
// in particular a failed address never probes again.
func (d *DAD) NextProbe() (pkt []byte, ok bool) {
	if d.state != StateTentative || d.sent >= d.cfg.Probes {
		return nil, false
	}
	nonce := make([]byte, NonceLen)
	if _, err := io.ReadFull(d.cfg.Rand, nonce); err != nil {
		return nil, false
	}
	d.nonces = append(d.nonces, nonce)
	d.sent++
	return marshalNS(d.addr, nonce), true
}

// Finish is called after the final probe interval elapsed with no
// conflict; it promotes the address to StateValid.
func (d *DAD) Finish() bool {
	if d.state == StateTentative && d.sent >= d.cfg.Probes {
		d.state = StateValid
	}
	return d.state == StateValid
}

// HandleMessage is called from the ND receive path for every NS and NA
// seen on the interface while the address is tentative.
func (d *DAD) HandleMessage(m Message) Verdict {
	if d.state != StateTentative {
		return VerdictIgnore
	}
	if m.IsNA {
		// An NA defends the address only when it names our tentative
		// target and could not have been forwarded (hop limit 255).
		if m.HopLimit == 255 && m.Target == d.addr {
			return d.conflict()
		}
		return VerdictIgnore
	}
	if m.Target != d.addr {
		return VerdictIgnore
	}
	if m.Src.IsUnspecified() && d.ownNonce(m.Nonce) {
		// RFC 7527: a DAD probe carrying one of our own nonces is our
		// probe looping back (or being replayed); it proves nothing
		// about other nodes.
		return VerdictLoopback
	}
	return d.conflict()
}

func (d *DAD) ownNonce(nonce []byte) bool {
	for _, n := range d.nonces {
		if bytes.Equal(n, nonce) {
			return true
		}
	}
	return false
}

func (d *DAD) conflict() Verdict {
	if d.retries < d.cfg.MaxRetries {
		d.retries++
		d.sent = 0
		d.nonces = d.nonces[:0]
		return VerdictConflict
	}
	d.state = StateFailed
	return VerdictConflict
}
