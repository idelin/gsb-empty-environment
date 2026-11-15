package netsec

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const (
	// AEADTagSize is the authentication tag size used by crypto/aes GCM.
	AEADTagSize = 16

	// MetadataSize is the encrypted final-slot header: an eight-byte result
	// length followed by one status byte.
	MetadataSize = 8 + 1

	// MinSlotBytes is the smallest slot that can hold both the GCM tag and the
	// final-slot metadata.
	MinSlotBytes = AEADTagSize + MetadataSize

	// NonceSize is the AES-GCM nonce size. A nonce is the big-endian uint32
	// round concatenated with the big-endian uint64 slot index.
	NonceSize = 4 + 8
)

const aadDomain = "example.org/netsec/q-125/shaped-output/v1"

// Plan is the public, fixed output plan. SlotBytes is the complete externally
// visible frame size, including the AES-GCM tag.
type Plan struct {
	Slots     uint64
	SlotBytes int
}

// Validate checks that the plan can carry authenticated final metadata and
// that its public result bound does not overflow uint64.
func (p Plan) Validate() error {
	if p.Slots == 0 {
		return fmt.Errorf("%w: slots must be positive", ErrInvalidPlan)
	}
	if p.SlotBytes < MinSlotBytes {
		return fmt.Errorf(
			"%w: slot size %d cannot hold a %d-byte GCM tag and %d-byte final metadata",
			ErrInvalidPlan,
			p.SlotBytes,
			AEADTagSize,
			MetadataSize,
		)
	}
	if p.Slots > 1 && uint64(p.SlotBytes) > math.MaxUint64/(p.Slots-1) {
		return fmt.Errorf("%w: result bound overflows uint64", ErrInvalidPlan)
	}
	return nil
}

// ResultLimit is the public upper bound (N-1)*L on submitted secret bytes.
// The per-slot GCM tag means the transport may have less encodable capacity;
// see EncodableBytes.
func (p Plan) ResultLimit() uint64 {
	if p.Slots == 0 {
		return 0
	}
	return (p.Slots - 1) * uint64(p.SlotBytes)
}

// DataBytesPerSlot is the number of secret-result bytes that fit in one
// non-final slot after reserving space for the GCM tag.
func (p Plan) DataBytesPerSlot() int {
	if p.SlotBytes <= AEADTagSize {
		return 0
	}
	return p.SlotBytes - AEADTagSize
}

// EncodableBytes is the total result capacity of the N-1 data slots. It is
// derived from the complete frame size and therefore accounts for one GCM tag
// per slot.
func (p Plan) EncodableBytes() uint64 {
	if p.Slots <= 1 || p.SlotBytes <= AEADTagSize {
		return 0
	}
	return (p.Slots - 1) * uint64(p.DataBytesPerSlot())
}

// Schedule describes when each public slot is due.
type Schedule struct {
	Start    time.Time
	Interval time.Duration
}

// Validate checks the public schedule.
func (s Schedule) Validate() error {
	if s.Interval <= 0 {
		return fmt.Errorf("%w: slot interval must be positive", ErrInvalidSchedule)
	}
	return nil
}

// SlotTime returns the scheduled time for slot. The caller is responsible for
// only using slots within the plan.
func (s Schedule) SlotTime(slot uint64) time.Time {
	return s.Start.Add(time.Duration(slot) * s.Interval)
}

func (s Schedule) validateFor(p Plan) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if p.Slots > 1 {
		maxSlots := uint64(math.MaxInt64 / int64(s.Interval))
		if p.Slots-1 > maxSlots {
			return fmt.Errorf("%w: final slot time overflows time.Duration", ErrInvalidSchedule)
		}
	}
	return nil
}

// Clock supplies the trusted time used to decide whether completion happened
// before the deadline. Tests can inject a controllable implementation.
type Clock interface {
	Now() time.Time
}

// SystemClock uses the process wall clock.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// NonceFor returns the exact AES-GCM nonce mandated by the protocol:
// big-endian uint32 round || big-endian uint64 slot.
func NonceFor(round uint32, slot uint64) []byte {
	nonce := make([]byte, NonceSize)
	binary.BigEndian.PutUint32(nonce[0:4], round)
	binary.BigEndian.PutUint64(nonce[4:12], slot)
	return nonce
}

// AssociatedData returns the canonical authenticated data binding the public
// plan, round, and slot. Slot is included so frames cannot be reordered even
// though the nonce already commits to it.
func AssociatedData(plan Plan, round uint32, slot uint64) []byte {
	aad := make([]byte, 0, len(aadDomain)+4+8+8+8)
	aad = append(aad, aadDomain...)
	aad = binary.BigEndian.AppendUint32(aad, round)
	aad = binary.BigEndian.AppendUint64(aad, plan.Slots)
	aad = binary.BigEndian.AppendUint64(aad, uint64(plan.SlotBytes))
	aad = binary.BigEndian.AppendUint64(aad, slot)
	return aad
}
