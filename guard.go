package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// DefaultMaxChainBytes is used when NewAPDUGuard is given a non-positive
	// chain limit.
	DefaultMaxChainBytes = 4096

	chainBit          = 0x10
	getResponseINS    = 0xC0
	moreDataSW1       = 0x61
	isoShortLe        = 256
	isoExtendedLeMax  = 65536
	apduHeaderLength  = 4
	responseStatusLen = 2
)

var (
	// ErrSessionBlocked means a chain violation has poisoned the session. The
	// forwarding loop must reject commands until ResetSession is called.
	ErrSessionBlocked = errors.New("netsec: APDU session blocked; reset required")

	// ErrMalformedAPDU means the command cannot be interpreted as an APDU.
	ErrMalformedAPDU = errors.New("netsec: malformed APDU")

	// ErrChainHeaderMismatch means a command in an active chain does not share
	// the chain's CLA/INS/P1/P2.
	ErrChainHeaderMismatch = errors.New("netsec: APDU chain header mismatch")

	// ErrChainTooLong means the serialized commands in a chain exceed the
	// configured limit.
	ErrChainTooLong = errors.New("netsec: APDU chain exceeds length limit")

	// ErrUnexpectedGetResponse means GET RESPONSE was sent without a preceding
	// 61xx status. Such a command can try to borrow stale card state and is
	// treated as a chain violation.
	ErrUnexpectedGetResponse = errors.New("netsec: GET RESPONSE without preceding 61xx")

	// ErrGetResponseTooLarge means GET RESPONSE asked for more bytes than the
	// preceding SW2 permits.
	ErrGetResponseTooLarge = errors.New("netsec: GET RESPONSE Le exceeds SW2")

	// ErrMalformedResponse means a card response was too short to contain a
	// status word.
	ErrMalformedResponse = errors.New("netsec: malformed card response")
)

// APDUGuard tracks ISO 7816 command chaining for a redirect proxy. The
// forwarding loop must call CheckCommand before sending each command and
// ObserveResponse after receiving each card response.
//
// When a chain is interrupted or otherwise violates the policy, the guard
// discards the chain and blocks every subsequent command until ResetSession is
// called. An APDUGuard is intended for one forwarding loop at a time and must
// not be used concurrently without external synchronization.
type APDUGuard struct {
	maxChainBytes int

	blocked bool

	inChain    bool
	chainCLA   byte
	chainINS   byte
	chainP1    byte
	chainP2    byte
	chainBytes int

	lastSW1         byte
	lastSW2         byte
	responsePending bool
}

// NewAPDUGuard returns a guard whose chained commands may total at most
// maxChainBytes serialized bytes, including headers and the final segment. A
// non-positive value selects DefaultMaxChainBytes.
func NewAPDUGuard(maxChainBytes int) *APDUGuard {
	if maxChainBytes <= 0 {
		maxChainBytes = DefaultMaxChainBytes
	}
	return &APDUGuard{maxChainBytes: maxChainBytes}
}

// CheckCommand reports whether the command may be forwarded to the card. It
// must be called before forwarding. On a policy violation it returns an error
// wrapping one of the package's sentinel errors and permanently discards the
// current chain until ResetSession is called.
func (g *APDUGuard) CheckCommand(apdu []byte) error {
	if g.blocked {
		return ErrSessionBlocked
	}
	if len(apdu) < apduHeaderLength {
		return g.rejectMalformed("command has %d bytes, need at least %d", len(apdu), apduHeaderLength)
	}

	cla, ins, p1, p2 := apdu[0], apdu[1], apdu[2], apdu[3]
	if ins == getResponseINS {
		return g.checkGetResponse(apdu, cla, p1, p2)
	}
	return g.checkCommand(apdu, cla, ins, p1, p2)
}

// ObserveResponse records the status word at the end of the card response. It
// must be called after every successfully forwarded command, including GET
// RESPONSE itself, so that a 61xx status can authorize the next GET RESPONSE.
func (g *APDUGuard) ObserveResponse(response []byte) error {
	if len(response) < responseStatusLen {
		g.responsePending = false
		g.lastSW1 = 0
		g.lastSW2 = 0
		return fmt.Errorf("%w: response has %d bytes, need a status word", ErrMalformedResponse, len(response))
	}
	g.lastSW1 = response[len(response)-responseStatusLen]
	g.lastSW2 = response[len(response)-1]
	g.responsePending = g.lastSW1 == moreDataSW1
	return nil
}

// ResetSession discards all chain and response state and unblocks the guard.
// The proxy calls it when a new card session has been established.
func (g *APDUGuard) ResetSession() {
	g.blocked = false
	g.inChain = false
	g.chainCLA = 0
	g.chainINS = 0
	g.chainP1 = 0
	g.chainP2 = 0
	g.chainBytes = 0
	g.lastSW1 = 0
	g.lastSW2 = 0
	g.responsePending = false
}

// Blocked reports whether the session has been poisoned by a chain violation.
func (g *APDUGuard) Blocked() bool {
	return g.blocked
}

func (g *APDUGuard) checkCommand(apdu []byte, cla, ins, p1, p2 byte) error {
	// A 61xx status belongs to the response immediately preceding the next
	// command. Once any other command is forwarded, that authorization is
	// stale and cannot be reused by a later GET RESPONSE.
	g.responsePending = false

	maskedCLA := cla &^ chainBit
	linked := cla&chainBit != 0

	if !g.inChain {
		if !linked {
			return nil
		}
		g.inChain = true
		g.chainCLA = maskedCLA
		g.chainINS = ins
		g.chainP1 = p1
		g.chainP2 = p2
		g.chainBytes = 0
	}

	if maskedCLA != g.chainCLA || ins != g.chainINS || p1 != g.chainP1 || p2 != g.chainP2 {
		return g.violate(fmt.Errorf(
			"%w: got CLA=%02X INS=%02X P1=%02X P2=%02X, want CLA=%02X INS=%02X P1=%02X P2=%02X",
			ErrChainHeaderMismatch,
			maskedCLA, ins, p1, p2,
			g.chainCLA, g.chainINS, g.chainP1, g.chainP2,
		))
	}

	g.chainBytes += len(apdu)
	if g.chainBytes > g.maxChainBytes {
		return g.violate(fmt.Errorf(
			"%w: %d bytes exceeds limit %d",
			ErrChainTooLong, g.chainBytes, g.maxChainBytes,
		))
	}

	if !linked {
		g.inChain = false
		g.chainBytes = 0
	}
	return nil
}

func (g *APDUGuard) checkGetResponse(apdu []byte, cla, p1, p2 byte) error {
	if cla&chainBit != 0 {
		return g.violate(fmt.Errorf("%w: GET RESPONSE may not be chained", ErrUnexpectedGetResponse))
	}
	if p1 != 0 || p2 != 0 {
		return g.violate(fmt.Errorf(
			"%w: GET RESPONSE requires P1=P2=00, got P1=%02X P2=%02X",
			ErrUnexpectedGetResponse, p1, p2,
		))
	}
	if !g.responsePending || g.lastSW1 != moreDataSW1 {
		return g.violate(fmt.Errorf(
			"%w: last status was %02X%02X",
			ErrUnexpectedGetResponse, g.lastSW1, g.lastSW2,
		))
	}

	le, err := getResponseLe(apdu)
	if err != nil {
		return g.violate(err)
	}

	available := int(g.lastSW2)
	if available == 0 {
		available = isoShortLe
	}
	if le > available {
		return g.violate(fmt.Errorf(
			"%w: Le=%d, SW2 permits %d bytes",
			ErrGetResponseTooLarge, le, available,
		))
	}

	// One 61xx status authorizes exactly one GET RESPONSE. The card response
	// to this command will establish whether another one is permitted.
	g.responsePending = false
	return nil
}

func (g *APDUGuard) rejectMalformed(format string, args ...any) error {
	err := fmt.Errorf("%w: "+format, append([]any{ErrMalformedAPDU}, args...)...)
	if g.inChain {
		return g.violate(err)
	}
	return err
}

// violate discards all in-flight chain state and poisons the session. The
// entire chain is dropped rather than merely rejecting the offending segment,
// so a later command cannot resume the partially authorized operation.
func (g *APDUGuard) violate(err error) error {
	g.blocked = true
	g.inChain = false
	g.chainCLA = 0
	g.chainINS = 0
	g.chainP1 = 0
	g.chainP2 = 0
	g.chainBytes = 0
	g.responsePending = false
	return err
}

func getResponseLe(apdu []byte) (int, error) {
	switch {
	case len(apdu) == 5:
		le := int(apdu[4])
		if le == 0 {
			le = isoShortLe
		}
		return le, nil
	case len(apdu) == 7 && apdu[4] == 0:
		le := int(binary.BigEndian.Uint16(apdu[5:7]))
		if le == 0 {
			le = isoExtendedLeMax
		}
		return le, nil
	default:
		return 0, fmt.Errorf(
			"%w: GET RESPONSE must carry one short or two extended Le bytes",
			ErrMalformedAPDU,
		)
	}
}
