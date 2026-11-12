package taskpkg

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// Admission records sent packet numbers and admits authenticated
// acknowledgments for one connection generation and direction.
//
// All exported methods are safe for concurrent use.
type Admission struct {
	mu         sync.Mutex
	auth       Authenticator
	id         Identity
	limit      uint64
	onReleased func(FrameID)

	sent        sentSet
	last        PacketNumber
	hasLast     bool
	outstanding map[PacketNumber][]FrameID
	refs        map[FrameID]int
}

// NewAdmission creates an admission controller. A nil Authenticator is an
// error: admission fails closed rather than accepting unauthenticated
// acknowledgments.
func NewAdmission(auth Authenticator, cfg Config) (*Admission, error) {
	if auth == nil {
		return nil, ErrNoAuthenticator
	}
	if cfg.Direction == 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidDirection, cfg.Direction)
	}
	return &Admission{
		auth:        auth,
		id:          Identity{Generation: cfg.Generation, Direction: cfg.Direction},
		limit:       cfg.PacketNumberLimit,
		onReleased:  cfg.OnFrameReleased,
		outstanding: make(map[PacketNumber][]FrameID),
		refs:        make(map[FrameID]int),
	}, nil
}

// New is an alias for NewAdmission.
func New(auth Authenticator, cfg Config) (*Admission, error) {
	return NewAdmission(auth, cfg)
}

// Identity returns the current generation and direction.
func (a *Admission) Identity() Identity {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.id
}

// Send records that packetNumber was sent and increments a reference for each
// distinct frame carried by it. Packet numbers must be strictly increasing
// within a generation, must be inside the configured space, and cannot be
// reused.
func (a *Admission) Send(packetNumber PacketNumber, frames ...FrameID) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendLocked(packetNumber, frames)
}

// SendNext records the next sequential packet number. If no number remains,
// it returns ErrPacketNumberExhausted and changes no state. The host must
// migrate to a new connection generation to continue.
func (a *Admission) SendNext(frames ...FrameID) (PacketNumber, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	var next PacketNumber
	if a.hasLast {
		max := a.maxNumber()
		if a.last >= max {
			return 0, fmt.Errorf("%w after packet number %d", ErrPacketNumberExhausted, a.last)
		}
		next = a.last + 1
	}
	if err := a.sendLocked(next, frames); err != nil {
		return 0, err
	}
	return next, nil
}

func (a *Admission) sendLocked(packetNumber PacketNumber, frames []FrameID) error {
	if packetNumber > a.maxNumber() {
		return fmt.Errorf("%w: packet number %d", ErrPacketNumberOutOfRange, packetNumber)
	}
	if a.hasLast {
		switch {
		case packetNumber == a.last || a.sent.contains(packetNumber):
			return fmt.Errorf("%w: packet number %d", ErrPacketNumberReused, packetNumber)
		case packetNumber < a.last:
			return fmt.Errorf("%w: packet number %d follows %d", ErrPacketNumberOutOfOrder, packetNumber, a.last)
		}
	}

	unique := make([]FrameID, 0, len(frames))
	seen := make(map[FrameID]struct{}, len(frames))
	for _, frame := range frames {
		if _, ok := seen[frame]; ok {
			continue
		}
		seen[frame] = struct{}{}
		unique = append(unique, frame)
	}

	a.sent.add(packetNumber)
	a.last = packetNumber
	a.hasLast = true
	for _, frame := range unique {
		a.refs[frame]++
	}
	a.outstanding[packetNumber] = unique
	return nil
}

func (a *Admission) maxNumber() PacketNumber {
	if a.limit == 0 {
		return PacketNumber(math.MaxUint64)
	}
	return PacketNumber(a.limit - 1)
}

// Exhausted reports whether no larger packet number exists in the current
// generation. The host must call Migrate before sending again.
func (a *Admission) Exhausted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hasLast && a.last >= a.maxNumber()
}

// LastSent returns the most recently sent packet number.
func (a *Admission) LastSent() (PacketNumber, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.hasLast
}

// IsSent reports whether the packet number was ever sent in the current
// generation, including packets whose acknowledgment was already admitted.
func (a *Admission) IsSent(packetNumber PacketNumber) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent.contains(packetNumber)
}

// SentIntervals returns the sparse set of sent packet numbers as sorted,
// disjoint closed intervals.
func (a *Admission) SentIntervals() []Interval {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sent.intervals()
}

// OutstandingPackets returns the number of sent packets not yet acknowledged.
func (a *Admission) OutstandingPackets() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.outstanding)
}

// PacketFrames returns the frame references held by an outstanding packet.
func (a *Admission) PacketFrames(packetNumber PacketNumber) ([]FrameID, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	frames, ok := a.outstanding[packetNumber]
	return append([]FrameID(nil), frames...), ok
}

// FrameRefs returns the number of outstanding packets that reference frameID.
func (a *Admission) FrameRefs(frameID FrameID) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refs[frameID]
}

// PendingFrames returns the frame IDs with a non-zero reference count.
func (a *Admission) PendingFrames() []FrameID {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingFramesLocked()
}

func (a *Admission) pendingFramesLocked() []FrameID {
	out := make([]FrameID, 0, len(a.refs))
	for frame, refs := range a.refs {
		if refs > 0 {
			out = append(out, frame)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// AdmitAck authenticates and admits one acknowledgment atomically.
//
// Authentication runs before any packet number or reference-count state is
// inspected. The message must name the host's current generation and
// direction. Every closed interval must be entirely contained in the set of
// packet numbers actually sent in that generation. If any interval is
// malformed or contains an unsent number, no state changes at all.
//
// Acknowledging the same packet or interval more than once is safe. Only the
// first admission of an outstanding packet decrements its frame references.
func (a *Admission) AdmitAck(msg AckMessage) (AckResult, error) {
	a.mu.Lock()
	expected := a.id
	auth := a.auth
	authMessage := cloneAckMessage(msg)
	a.mu.Unlock()

	if err := auth.Authenticate(authMessage, expected); err != nil {
		return AckResult{}, fmt.Errorf("%w: %w", ErrAuthentication, err)
	}

	a.mu.Lock()
	if a.id != expected {
		a.mu.Unlock()
		return AckResult{}, ErrConnectionChanged
	}

	result, err := a.admitLocked(msg)
	a.mu.Unlock()

	if err == nil && a.onReleased != nil {
		for _, frame := range result.ReleasedFrames {
			a.onReleased(frame)
		}
	}
	return result, err
}

func (a *Admission) admitLocked(msg AckMessage) (AckResult, error) {
	if msg.Generation != a.id.Generation {
		return AckResult{}, fmt.Errorf("%w: got %d, want %d",
			ErrGenerationMismatch, msg.Generation, a.id.Generation)
	}
	if msg.Direction != a.id.Direction {
		return AckResult{}, fmt.Errorf("%w: got %s, want %s",
			ErrDirectionMismatch, msg.Direction, a.id.Direction)
	}

	for i, interval := range msg.Intervals {
		if !interval.Valid() {
			return AckResult{}, &MalformedIntervalError{Index: i, Interval: interval}
		}
	}

	var rejected *UnsentPacketError
	for i, interval := range msg.Intervals {
		missing, ok := a.sent.firstMissing(interval.First, interval.Last)
		if !ok {
			continue
		}
		if rejected == nil || missing < rejected.Number {
			rejected = &UnsentPacketError{
				Index:    i,
				Interval: interval,
				Number:   missing,
			}
		}
		if missing == 0 {
			break
		}
	}
	if rejected != nil {
		return AckResult{Rejected: rejected}, rejected
	}

	union := mergeIntervals(msg.Intervals)
	acknowledged := make([]PacketNumber, 0)
	for packetNumber := range a.outstanding {
		if unionContains(union, packetNumber) {
			acknowledged = append(acknowledged, packetNumber)
		}
	}
	sort.Slice(acknowledged, func(i, j int) bool {
		return acknowledged[i] < acknowledged[j]
	})

	released := make([]FrameID, 0)
	for _, packetNumber := range acknowledged {
		frames := a.outstanding[packetNumber]
		delete(a.outstanding, packetNumber)
		for _, frame := range frames {
			refs := a.refs[frame]
			if refs <= 1 {
				delete(a.refs, frame)
				released = append(released, frame)
				continue
			}
			a.refs[frame] = refs - 1
		}
	}
	sort.Slice(released, func(i, j int) bool { return released[i] < released[j] })

	return AckResult{
		Admitted:            true,
		AcknowledgedPackets: len(acknowledged),
		Packets:             acknowledged,
		ReleasedFrames:      released,
	}, nil
}

// Migrate starts a new connection generation. The generation must be greater
// than the current one; packet number state is not reused or wrapped.
//
// All outstanding packets and frame references from the old connection are
// abandoned. Their frame IDs are returned in MigrationResult so the host can
// release or resend them. Acknowledgment callbacks are not invoked for
// abandoned frames.
func (a *Admission) Migrate(next Generation, direction Direction) (MigrationResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if direction == 0 {
		return MigrationResult{}, fmt.Errorf("%w: %d", ErrInvalidDirection, direction)
	}
	if next <= a.id.Generation {
		return MigrationResult{}, fmt.Errorf("%w: %d follows %d",
			ErrGenerationNotAdvanced, next, a.id.Generation)
	}

	result := MigrationResult{
		From:             a.id.Generation,
		To:               next,
		AbandonedPackets: len(a.outstanding),
		AbandonedFrames:  a.pendingFramesLocked(),
	}

	a.id = Identity{Generation: next, Direction: direction}
	a.sent = sentSet{}
	a.last = 0
	a.hasLast = false
	a.outstanding = make(map[PacketNumber][]FrameID)
	a.refs = make(map[FrameID]int)
	return result, nil
}
