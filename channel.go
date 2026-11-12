package netsec

import (
	"errors"
	"sync"
)

// DefaultReplayWindow is the number of recent packet numbers retained for
// anti-replay checking, matching the IEEE 802.1AE default protection window.
const DefaultReplayWindow = 64

// maxReplayWindow bounds the window to the width of the internal bitmap.
const maxReplayWindow = 64

// maxAssociationNumber is the largest value of the 2-bit SecTAG AN field.
const maxAssociationNumber = 3

// KeyID is the Key Identifier (KI) that MKA binds to a distributed SAK. The
// identifier is treated as an opaque comparable token.
type KeyID string

// SAK is a Secure Association Key distributed by MKA. Index carries the key
// numbering used by the key server; Key holds the raw key material.
type SAK struct {
	Index uint16
	Key   []byte
}

// SecTag carries the fields of a received MACsec SecTAG that are relevant to
// receive-channel selection and replay protection.
type SecTag struct {
	SCI               SCI
	AssociationNumber uint8
	PacketNumber      uint32
}

// Sentinel errors returned by the receive channel table. Errors may be wrapped
// with context, so callers should compare using errors.Is rather than '=='.
var (
	ErrBufferTooShort      = errors.New("netsec: buffer too short")
	ErrInvalidSCI          = errors.New("netsec: invalid SCI encoding")
	ErrUnknownSCI          = errors.New("netsec: SCI not advertised by MKA")
	ErrInvalidAN           = errors.New("netsec: invalid association number")
	ErrNoSecureAssociation = errors.New("netsec: no secure association for AN")
	ErrAssociationExists   = errors.New("netsec: active secure association already exists")
	ErrRetiredSAK          = errors.New("netsec: secure association key retired")
	ErrStaleSAK            = errors.New("netsec: stale or mismatched key identifier")
	ErrReplay              = errors.New("netsec: replay or out-of-window packet number")
	ErrInvalidPacketNumber = errors.New("netsec: packet number must be non-zero")
	ErrICV                 = errors.New("netsec: ICV verification failed")
)

// RxTable is the receive-side channel table consulted immediately before
// decapsulation. A frame is accepted only when its SCI was advertised by MKA
// and is bound to an installed, non-retired SA whose KI matches the key MKA
// distributed for that secure channel.
type RxTable struct {
	mu       sync.RWMutex
	channels map[SCI]*rxChannel
	window   uint
}

// rxChannel is the per-SCI receive state holding its secure associations.
type rxChannel struct {
	active [maxAssociationNumber + 1]*RxSA
}

// RxSA is one receive secure association: an SCI/AN pair bound to a KI/SAK and
// protected by a 32-bit sliding packet-number window.
type RxSA struct {
	sci     SCI
	an      uint8
	ki      KeyID
	sak     SAK
	window  uint
	retired bool

	mu        sync.Mutex
	highestPN uint32
	bitmap    uint64
}

// Option configures a receive table at construction.
type Option func(*RxTable)

// WithReplayWindow overrides the anti-replay window size. n must be in 1..64;
// out-of-range values are ignored in favor of the default.
func WithReplayWindow(n int) Option {
	return func(t *RxTable) {
		if n < 1 || n > maxReplayWindow {
			return
		}
		t.window = uint(n)
	}
}

// NewRxTable creates an empty receive channel table.
func NewRxTable(opts ...Option) *RxTable {
	t := &RxTable{
		channels: make(map[SCI]*rxChannel),
		window:   DefaultReplayWindow,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// LearnSCI registers an SCI advertised by MKA. Registration is idempotent:
// re-advertising an already-known SCI is a no-op. No SA is installed until
// InstallSA binds a KI/SAK to it, so a learned-but-unbound SCI cannot receive.
func (t *RxTable) LearnSCI(sci SCI) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.channels[sci]; ok {
		return nil
	}
	t.channels[sci] = &rxChannel{}
	return nil
}

// ForgetSCI removes an SCI and all of its associations after MKA tears the
// secure channel down.
func (t *RxTable) ForgetSCI(sci SCI) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.channels[sci]; !ok {
		return ErrUnknownSCI
	}
	delete(t.channels, sci)
	return nil
}

// Learned reports whether MKA has registered the SCI.
func (t *RxTable) Learned(sci SCI) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.channels[sci]
	return ok
}

// ChannelCount returns the number of MKA-registered receive channels.
func (t *RxTable) ChannelCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.channels)
}

// InstallSA binds a freshly distributed SAK (identified by ki) to the given
// SCI and association number. The SCI must already be advertised by MKA,
// otherwise ErrUnknownSCI is returned; this is what blocks forged SCIs from
// ever reaching the replay layer. Replacing a retired association is allowed;
// replacing an active one returns ErrAssociationExists.
func (t *RxTable) InstallSA(sci SCI, an uint8, ki KeyID, sak SAK) (*RxSA, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.channels[sci]
	if !ok {
		return nil, ErrUnknownSCI
	}
	if an > maxAssociationNumber {
		return nil, ErrInvalidAN
	}
	if existing := ch.active[an]; existing != nil && !existing.isRetired() {
		return nil, ErrAssociationExists
	}

	sa := &RxSA{
		sci:    sci,
		an:     an,
		ki:     ki,
		sak:    sak,
		window: t.window,
	}
	ch.active[an] = sa
	return sa, nil
}

// Rekey performs a make-before-break key rollover for sci. The current
// association at oldAN must be active; a new association carrying ki/sak is
// installed at newAN while the old one remains valid during the overlap. The
// caller retires oldAN with RetireSA once peers have switched.
func (t *RxTable) Rekey(sci SCI, oldAN, newAN uint8, ki KeyID, sak SAK) (*RxSA, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.channels[sci]
	if !ok {
		return nil, ErrUnknownSCI
	}
	if oldAN > maxAssociationNumber || newAN > maxAssociationNumber {
		return nil, ErrInvalidAN
	}
	if oldAN == newAN {
		return nil, ErrInvalidAN
	}
	old := ch.active[oldAN]
	if old == nil {
		return nil, ErrNoSecureAssociation
	}
	if old.isRetired() {
		return nil, ErrRetiredSAK
	}
	if target := ch.active[newAN]; target != nil && !target.isRetired() {
		return nil, ErrAssociationExists
	}

	newSA := &RxSA{
		sci:    sci,
		an:     newAN,
		ki:     ki,
		sak:    sak,
		window: t.window,
	}
	ch.active[newAN] = newSA
	return newSA, nil
}

// RetireSA marks the association at sci/an as no longer permitted to receive.
// Its retained entry makes subsequent frames fail with ErrRetiredSAK rather
// than being silently confused with an unconfigured AN.
func (t *RxTable) RetireSA(sci SCI, an uint8) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch, ok := t.channels[sci]
	if !ok {
		return ErrUnknownSCI
	}
	if an > maxAssociationNumber {
		return ErrInvalidAN
	}
	sa := ch.active[an]
	if sa == nil {
		return ErrNoSecureAssociation
	}
	return sa.retire()
}

// Lookup selects the active SA for a received frame's SCI and AN. It performs
// the authorization step that must precede decapsulation:
//   - an SCI never advertised by MKA yields ErrUnknownSCI (forged SCI);
//   - an AN without an installed SA yields ErrNoSecureAssociation;
//   - an AN whose SAK was retired yields ErrRetiredSAK.
//
// The returned association is then used for ICV verification and replay checks.
func (t *RxTable) Lookup(sci SCI, an uint8) (*RxSA, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	ch, ok := t.channels[sci]
	if !ok {
		return nil, ErrUnknownSCI
	}
	if an > maxAssociationNumber {
		return nil, ErrInvalidAN
	}
	sa := ch.active[an]
	if sa == nil {
		return nil, ErrNoSecureAssociation
	}
	if sa.isRetired() {
		return nil, ErrRetiredSAK
	}
	return sa, nil
}

// Receive runs the full pre-decapsulation gate for one frame: channel
// authorization, ICV gating and anti-replay processing. The replay window is
// advanced only when icvOK is true and the packet number is acceptable, so a
// failed ICV never mutates receive state.
func (t *RxTable) Receive(tag SecTag, icvOK bool) error {
	sa, err := t.Lookup(tag.SCI, tag.AssociationNumber)
	if err != nil {
		return err
	}
	if !icvOK {
		return ErrICV
	}
	if err := sa.CheckPN(tag.PacketNumber); err != nil {
		return err
	}
	return sa.Commit(tag.PacketNumber)
}

// SCI returns the secure channel identifier this association belongs to.
func (s *RxSA) SCI() SCI { return s.sci }

// AssociationNumber returns the AN selecting this association in the SecTAG.
func (s *RxSA) AssociationNumber() uint8 { return s.an }

// BoundKeyID returns the KI MKA bound to this association's SAK.
func (s *RxSA) BoundKeyID() KeyID { return s.ki }

// SAK returns the key material installed for this association.
func (s *RxSA) SAK() SAK { return s.sak }

// VerifyKeyID reports whether ki is the identifier MKA bound to this SA. A
// mismatch means the frame references a superseded or forged key.
func (s *RxSA) VerifyKeyID(ki KeyID) error {
	if ki != s.ki {
		return ErrStaleSAK
	}
	return nil
}

// Window returns the anti-replay window size in packets.
func (s *RxSA) Window() int { return int(s.window) }

// HighestPN returns the greatest accepted packet number.
func (s *RxSA) HighestPN() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.highestPN
}

// isRetired reports the retirement flag. Callers hold the table lock or the
// association lock.
func (s *RxSA) isRetired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retired
}

// retire marks the association retired. Retiring an already-retired SA errors.
func (s *RxSA) retire() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired {
		return ErrRetiredSAK
	}
	s.retired = true
	return nil
}

// CheckPN applies the 32-bit sliding-window anti-replay check without mutating
// state. It is called before the ICV result is committed; a non-incrementing
// duplicate or a packet number older than the window returns ErrReplay, and
// the reserved packet number zero returns ErrInvalidPacketNumber.
func (s *RxSA) CheckPN(pn uint32) error {
	if pn == 0 {
		return ErrInvalidPacketNumber
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.retired {
		return ErrRetiredSAK
	}

	// Signed 32-bit difference keeps the check correct across PN wrap-around.
	diff := int32(pn - s.highestPN)
	switch {
	case diff > 0:
		return nil
	case diff == 0:
		// Packet number equal to the highest is always a retransmission.
		return ErrReplay
	default:
		offset := uint64(-diff)
		if offset >= uint64(s.window) {
			return ErrReplay
		}
		if s.bitmap&(1<<offset) != 0 {
			return ErrReplay
		}
		return nil
	}
}

// Commit advances the sliding window after an ICV has been verified and
// CheckPN accepted the packet number. Committing an accepted number records it
// in the window bitmap. The caller is expected to have run CheckPN first.
func (s *RxSA) Commit(pn uint32) error {
	if pn == 0 {
		return ErrInvalidPacketNumber
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.retired {
		return ErrRetiredSAK
	}

	diff := int32(pn - s.highestPN)
	switch {
	case diff > 0:
		if uint(diff) >= maxReplayWindow {
			s.bitmap = 0
		} else {
			s.bitmap <<= uint(diff)
		}
		s.highestPN = pn
		s.bitmap |= 1
	case diff == 0:
		// Already recorded by the prior commit; nothing to do.
	default:
		offset := uint64(-diff)
		if offset < uint64(s.window) {
			s.bitmap |= 1 << offset
		}
	}
	return nil
}
