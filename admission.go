package netsec

import (
	"errors"
	"fmt"
	"net/netip"
)

var (
	// ErrSegmentsLeftExceedsLastEntry reports an SRH with
	// Segments Left > Last Entry + 1.
	ErrSegmentsLeftExceedsLastEntry = errors.New("netsec: Segments Left exceeds Last Entry + 1")

	// ErrMissingHMAC reports an external SRH without an HMAC TLV.
	ErrMissingHMAC = errors.New("netsec: external SRH has no HMAC TLV")

	// ErrZeroHMACKeyID reports an HMAC TLV with Key ID 0.
	ErrZeroHMACKeyID = errors.New("netsec: SRH HMAC Key ID is zero")

	// ErrUnknownHMACKeyID reports a nonzero Key ID that the local key store
	// does not know.
	ErrUnknownHMACKeyID = errors.New("netsec: unknown SRH HMAC Key ID")

	// ErrUnsupportedHMAC reports an HMAC TLV that is not HMAC-SHA256 sized.
	ErrUnsupportedHMAC = errors.New("netsec: unsupported SRH HMAC TLV")

	// ErrInvalidHMAC reports an HMAC-SHA256 mismatch.
	ErrInvalidHMAC = errors.New("netsec: SRH HMAC verification failed")
)

// HMACKeyFunc resolves an SRH HMAC Key ID to a pre-shared HMAC-SHA256 key.
// Returning ok=false or a zero-length key denies the packet.
type HMACKeyFunc func(keyID uint32) (key []byte, ok bool)

// HMACKeys is a static Key ID to pre-shared-key map.
type HMACKeys map[uint32][]byte

// Lookup implements the key resolver expected by AdmitExternalSRH.
func (k HMACKeys) Lookup(keyID uint32) ([]byte, bool) {
	key, ok := k[keyID]
	if !ok || len(key) == 0 {
		return nil, false
	}
	return key, true
}

// AdmitExternalSRH applies the trust-domain boundary policy to an IPv6 packet
// arriving from outside the SR domain.
//
// A packet without an SRH is admitted.  A packet with an SRH is admitted only
// when all of the following hold:
//
//   - Segments Left <= Last Entry + 1.
//   - Exactly one HMAC TLV is present.
//   - The HMAC TLV Key ID is nonzero and known to keyFor.
//   - The HMAC TLV carries a 32-octet HMAC-SHA256 over the source address,
//     Last Entry, Flags, two zero reserved octets, Key ID, and the full
//     Segment List.
//
// On denial, the returned error wraps one of the Err* sentinels in this
// package, so callers can use errors.Is to select a drop counter.
func AdmitExternalSRH(packet []byte, keyFor HMACKeyFunc) error {
	src, srh, err := FindSRH(packet)
	if err != nil {
		return err
	}
	if srh == nil {
		return nil
	}
	return admitSRH(srh, src, keyFor)
}

// AdmitExternalSRHHeader applies the same policy to an already-isolated SRH
// and its IPv6 source address.  srh may include bytes following the SRH.
func AdmitExternalSRHHeader(srh []byte, src netip.Addr, keyFor HMACKeyFunc) error {
	parsed, err := ParseSRH(srh)
	if err != nil {
		return err
	}
	return admitSRH(parsed, src, keyFor)
}

func admitSRH(srh *SRH, src netip.Addr, keyFor HMACKeyFunc) error {
	if int(srh.SegmentsLeft) > int(srh.LastEntry)+1 {
		return fmt.Errorf("%w: Segments Left %d, Last Entry %d",
			ErrSegmentsLeftExceedsLastEntry, srh.SegmentsLeft, srh.LastEntry)
	}

	if srh.HMAC == nil {
		return ErrMissingHMAC
	}
	if srh.HMAC.KeyID == 0 {
		return ErrZeroHMACKeyID
	}
	if len(srh.HMAC.HMAC) != HMACSHA256Size {
		return fmt.Errorf("%w: HMAC length %d, want %d",
			ErrUnsupportedHMAC, len(srh.HMAC.HMAC), HMACSHA256Size)
	}
	if srh.HMAC.Reserved != 0 {
		return fmt.Errorf("%w: HMAC reserved bits %#04x", ErrUnsupportedHMAC, srh.HMAC.Reserved)
	}
	if keyFor == nil {
		return fmt.Errorf("%w: %d", ErrUnknownHMACKeyID, srh.HMAC.KeyID)
	}

	key, ok := keyFor(srh.HMAC.KeyID)
	if !ok || len(key) == 0 {
		return fmt.Errorf("%w: %d", ErrUnknownHMACKeyID, srh.HMAC.KeyID)
	}
	if !srh.VerifyHMACSHA256(src, key) {
		return fmt.Errorf("%w: Key ID %d", ErrInvalidHMAC, srh.HMAC.KeyID)
	}

	return nil
}
