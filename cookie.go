package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/netip"
	"time"
)

const (
	cookieLowMask  = uint32(0x00ffffff)
	cookieMSSShift = 5
	cookieWSShift  = 1
	cookieSACKMask = uint32(1 << 24)

	// The cookie has no spare sentinel for an absent MSS option. The HMAC
	// input therefore sets the top bit of the MSS index byte when the option
	// is absent. The actual 3-bit index still occupies the high cookie bits,
	// so all eight cookie bits stay authenticated and a missing option is
	// never restored as table[0].
	mssAbsentIndex = byte(0)
	mssAbsentTag   = byte(0x80)
)

// Host holds the key material and configuration used to issue and verify SYN
// cookies.
//
// One key generation lasts one time slot. At any instant the current key is
// valid for cookies issued in the current slot and the previous key is valid
// for cookies issued in the immediately preceding slot. Slots are maintained
// as 64-bit values; the low eight bits form the generation tag, while the
// complete slot is authenticated by the cookie. Using the complete slot is
// what makes a cookie from the slot before an uint8 wrap-around
// distinguishable from a cookie issued 256 slots ago.
type Host struct {
	currentKey  [32]byte
	previousKey [32]byte
	slot        time.Duration
	mssTable    []uint16
	now         func() time.Time
}

// NewHost creates a cookie host.
//
// currentKey and previousKey are 32-byte secret keys for the current and
// previous key generation. slot is the duration of one time slot. mssTable
// contains at most eight non-zero MSS values. The table does not need to be
// sorted.
func NewHost(currentKey, previousKey [32]byte, slot time.Duration, mssTable []uint16) (*Host, error) {
	if slot <= 0 {
		return nil, fmt.Errorf("netsec: slot duration must be positive")
	}
	if len(mssTable) > 8 {
		return nil, fmt.Errorf("netsec: MSS table has %d entries, maximum is 8", len(mssTable))
	}
	for i, mss := range mssTable {
		if mss == 0 {
			return nil, fmt.Errorf("netsec: MSS table entry %d is zero", i)
		}
	}

	table := append([]uint16(nil), mssTable...)
	return &Host{
		currentKey:  currentKey,
		previousKey: previousKey,
		slot:        slot,
		mssTable:    table,
		now:         time.Now,
	}, nil
}

// SetNowFunc replaces the clock used by GenerateCookie and VerifyCookie. It is
// primarily useful for deterministic tests. Passing nil restores time.Now.
func (h *Host) SetNowFunc(now func() time.Time) {
	if now == nil {
		now = time.Now
	}
	h.now = now
}

// RotateKeys installs a new current key and demotes the old current key to the
// previous generation. This should be called when a generation boundary is
// crossed; the host does not derive new key material itself.
func (h *Host) RotateKeys(current [32]byte) {
	h.previousKey = h.currentKey
	h.currentKey = current
}

// SlotAt returns the host's 64-bit slot number for t. Slots start at the Unix
// epoch; negative times are rounded towards negative infinity.
func (h *Host) SlotAt(t time.Time) uint64 {
	nanos := t.UnixNano()
	d := int64(h.slot)
	q := nanos / d
	if nanos%d != 0 && nanos < 0 {
		q--
	}
	return uint64(q)
}

// CurrentSlot returns the slot number for the host's current clock value.
func (h *Host) CurrentSlot() uint64 {
	return h.SlotAt(h.now())
}

// GenerateCookie creates a cookie for a SYN directed at this host.
//
// src is the client endpoint and dst is the server endpoint; both must be
// valid. Only options present in opts are authenticated and encoded.
func (h *Host) GenerateCookie(src, dst netip.AddrPort, clientISN uint32, opts SYNOptions) (uint32, error) {
	return h.GenerateCookieAt(h.now(), src, dst, clientISN, opts)
}

// GenerateCookieAt is GenerateCookie with an explicit clock value.
func (h *Host) GenerateCookieAt(now time.Time, src, dst netip.AddrPort, clientISN uint32, opts SYNOptions) (uint32, error) {
	if !src.IsValid() || !dst.IsValid() {
		return 0, fmt.Errorf("netsec: invalid endpoint")
	}
	if err := opts.validate(); err != nil {
		return 0, err
	}

	sack := byte(0)
	if opts.SACKPermitted {
		sack = 1
	}
	windowScale := byte(windowScaleAbsent)
	if opts.WindowScale != nil {
		windowScale = *opts.WindowScale
	}

	mssIndex := mssAbsentIndex
	mssPresent := opts.MSS != nil
	if mssPresent {
		index, ok := h.mssIndex(*opts.MSS)
		if !ok {
			return 0, fmt.Errorf("%w: advertised %d", ErrNoMSSMatch, *opts.MSS)
		}
		mssIndex = index
	}

	slot := h.SlotAt(now)
	digest := h.mac(slot, src, dst, clientISN, mssPresent, mssIndex, windowScale, sack)
	low := uint32(digest[29])<<16 | uint32(digest[30])<<8 | uint32(digest[31])
	high := mssIndex<<cookieMSSShift | windowScale<<cookieWSShift | sack

	return uint32(high)<<24 | low, nil
}

// VerifyCookie checks a cookie and the ACK number that acknowledges it.
//
// The returned options are exactly the options encoded in the authenticated
// high byte. An absent window scale or SACK permission is returned as absent;
// it is never inferred from host defaults or the MSS table. The MSS value is
// the table entry selected when the cookie was issued. Verification additionally
// accepts a cookie issued by the previous key generation during exactly one
// following slot.
func (h *Host) VerifyCookie(cookie, ack uint32, src, dst netip.AddrPort, clientISN uint32) (SYNOptions, error) {
	return h.VerifyCookieAt(h.now(), cookie, ack, src, dst, clientISN)
}

// VerifyCookieAt is VerifyCookie with an explicit clock value.
func (h *Host) VerifyCookieAt(now time.Time, cookie, ack uint32, src, dst netip.AddrPort, clientISN uint32) (SYNOptions, error) {
	if !src.IsValid() || !dst.IsValid() {
		return SYNOptions{}, ErrBadCookie
	}
	if !acknowledgesCookie(cookie, ack) {
		return SYNOptions{}, ErrBadACK
	}

	mssIndex := byte((cookie >> 29) & 0x7)
	windowScale := byte((cookie >> 25) & 0xf)
	sack := byte((cookie & cookieSACKMask) >> 24)
	actual := [3]byte{byte((cookie & cookieLowMask) >> 16), byte(cookie >> 8), byte(cookie)}

	slot := h.SlotAt(now)
	type candidate struct {
		key    [32]byte
		slot   uint64
		usable bool
	}
	candidates := [2]candidate{
		{key: h.currentKey, slot: slot, usable: true},
		{key: h.previousKey, slot: slot - 1, usable: slot > 0},
	}

	var presentHits, absentHits [2]bool
	for i, candidate := range candidates {
		if !candidate.usable {
			continue
		}
		present := h.macWithKey(candidate.key, candidate.slot, src, dst, clientISN, true, mssIndex, windowScale, sack)
		absent := h.macWithKey(candidate.key, candidate.slot, src, dst, clientISN, false, mssIndex, windowScale, sack)
		presentHits[i] = subtle.ConstantTimeCompare(actual[:], present[29:]) == 1
		absentHits[i] = subtle.ConstantTimeCompare(actual[:], absent[29:]) == 1
	}

	present := presentHits[0] || presentHits[1]
	absent := absentHits[0] || absentHits[1]
	if present && absent {
		// A 24-bit collision between the present and absent forms is
		// extraordinarily unlikely. Refuse to guess rather than risk
		// restoring an MSS option that was not present.
		return SYNOptions{}, ErrBadCookie
	}
	if !present && !absent {
		return SYNOptions{}, ErrBadCookie
	}

	var opts SYNOptions
	if present {
		if int(mssIndex) >= len(h.mssTable) {
			return SYNOptions{}, ErrBadCookie
		}
		mss := h.mssTable[mssIndex]
		opts.MSS = &mss
	} else if mssIndex != mssAbsentIndex {
		return SYNOptions{}, ErrBadCookie
	}

	if windowScale == windowScaleAbsent {
		opts.WindowScale = nil
	} else {
		scale := windowScale
		opts.WindowScale = &scale
	}
	opts.SACKPermitted = sack == 1

	return opts, nil
}

// acknowledgesCookie performs the ACK check with explicit uint32 wrap-around,
// so a cookie sequence value of 0xffffffff is acknowledged by zero.
func acknowledgesCookie(cookie, ack uint32) bool {
	return ack == cookie+1
}

func (h *Host) mssIndex(advertised uint16) (byte, bool) {
	best := -1
	for i, candidate := range h.mssTable {
		if candidate > advertised {
			continue
		}
		if best < 0 || candidate > h.mssTable[best] {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	return byte(best), true
}

func (h *Host) mac(slot uint64, src, dst netip.AddrPort, clientISN uint32, mssPresent bool, mssIndex, windowScale, sack byte) [32]byte {
	return h.macWithKey(h.currentKey, slot, src, dst, clientISN, mssPresent, mssIndex, windowScale, sack)
}

func (h *Host) macWithKey(key [32]byte, slot uint64, src, dst netip.AddrPort, clientISN uint32, mssPresent bool, mssIndex, windowScale, sack byte) [32]byte {
	// All fields are fixed width, so no separators or length prefixes are
	// needed. IPv4 addresses use their canonical IPv4-mapped 16-byte form.
	input := make([]byte, 0, 16+2+16+2+4+8+3)
	source := src.Addr().As16()
	input = append(input, source[:]...)
	input = binary.BigEndian.AppendUint16(input, src.Port())
	destination := dst.Addr().As16()
	input = append(input, destination[:]...)
	input = binary.BigEndian.AppendUint16(input, dst.Port())
	input = binary.BigEndian.AppendUint32(input, clientISN)
	input = binary.BigEndian.AppendUint64(input, slot)
	mssTag := mssIndex
	if !mssPresent {
		mssTag |= mssAbsentTag
	}
	input = append(input, mssTag, windowScale, sack)

	mac := hmac.New(sha256.New, key[:])
	mac.Write(input)
	sum := mac.Sum(nil)
	return [32]byte(sum)
}
