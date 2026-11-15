package netsec

import (
	"encoding/binary"
	"fmt"
)

// DecodedResult is the authenticated secret recovered from a complete frame
// sequence.
type DecodedResult struct {
	Data    []byte
	Length  uint64
	Success bool
}

// OpenFrames authenticates and decrypts a complete public trajectory. Frames
// must be in slot order, use the supplied round, and have exactly the planned
// size.
func OpenFrames(plan Plan, round uint32, key *Key, frames []Frame) (*DecodedResult, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidKey)
	}
	if plan.Slots > uint64(maxInt()) {
		return nil, fmt.Errorf("%w: frame count does not fit int", ErrFrameCount)
	}
	if uint64(len(frames)) != plan.Slots {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrFrameCount, len(frames), plan.Slots)
	}

	capacity := plan.EncodableBytes()
	if capacity > uint64(maxInt()) {
		return nil, fmt.Errorf("%w: result capacity does not fit int", ErrFrameCount)
	}
	stream := make([]byte, 0, int(capacity))

	var (
		length  uint64
		success bool
	)

	for i, frame := range frames {
		slot := uint64(i)
		if frame.Round != round {
			return nil, fmt.Errorf("%w: slot %d has round %d, want %d", ErrFrameLayout, slot, frame.Round, round)
		}
		if frame.Slot != slot {
			return nil, fmt.Errorf("%w: frame %d claims slot %d", ErrFrameLayout, i, frame.Slot)
		}
		if len(frame.Data) != plan.SlotBytes {
			return nil, fmt.Errorf(
				"%w: slot %d is %d bytes, want %d",
				ErrFrameLayout,
				slot,
				len(frame.Data),
				plan.SlotBytes,
			)
		}

		plaintext, err := key.open(
			NonceFor(round, slot),
			frame.Data,
			AssociatedData(plan, round, slot),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: slot %d", ErrAuthentication, slot)
		}

		if slot < plan.Slots-1 {
			stream = append(stream, plaintext...)
			continue
		}
		if len(plaintext) < MetadataSize {
			return nil, fmt.Errorf("%w: final plaintext too short", ErrMalformedMetadata)
		}
		length = binary.BigEndian.Uint64(plaintext[0:8])
		switch plaintext[8] {
		case 0:
			success = false
		case 1:
			success = true
		default:
			return nil, fmt.Errorf("%w: status byte %d", ErrMalformedMetadata, plaintext[8])
		}
	}

	if length > uint64(len(stream)) {
		return nil, fmt.Errorf("%w: length %d exceeds %d transported bytes", ErrMalformedMetadata, length, len(stream))
	}
	if length > plan.ResultLimit() {
		return nil, fmt.Errorf("%w: length %d exceeds public bound %d", ErrMalformedMetadata, length, plan.ResultLimit())
	}

	start := len(stream) - int(length)
	return &DecodedResult{
		Data:    append([]byte(nil), stream[start:]...),
		Length:  length,
		Success: success,
	}, nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
