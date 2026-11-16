package netsec

import (
	"fmt"
	"io"
)

// FrameWriter emits MLLP-framed HL7 messages. It never "escapes" framing
// bytes: MLLP has no escaping mechanism, so any payload containing VT or FS
// is rejected before a single byte hits the wire.
//
// A FrameWriter is not safe for concurrent use.
type FrameWriter struct {
	w io.Writer
}

// NewFrameWriter wraps w.
func NewFrameWriter(w io.Writer) *FrameWriter {
	return &FrameWriter{w: w}
}

// ValidatePayload enforces the writer-side rules:
//
//   - VT (0x0B) and FS (0x1C) are forbidden anywhere in the payload;
//   - CR (0x0D) is a segment terminator and may only appear as the last byte
//     or immediately before the next segment identifier (an uppercase ASCII
//     letter, as used by HL7 segment names such as MSH and PID).
//
// An empty payload is allowed (a keep-alive style empty frame).
func ValidatePayload(payload []byte) error {
	for i := 0; i < len(payload); i++ {
		switch payload[i] {
		case VT, FS:
			return fmt.Errorf("%w: 0x%02X at byte %d", ErrFramingByte, payload[i], i)
		case CR:
			if i == len(payload)-1 {
				continue // final segment terminator
			}
			next := payload[i+1]
			if !isASCIILetter(next) {
				return fmt.Errorf("%w: CR at byte %d followed by 0x%02X, not a segment identifier",
					ErrCRPosition, i, next)
			}
		}
	}
	return nil
}

// WriteFrame validates payload and writes VT + payload + FS CR as a single
// Write call so a frame never interleaves with other writers on the same
// connection. It returns the validation error without writing anything.
func (fw *FrameWriter) WriteFrame(payload []byte) error {
	if err := ValidatePayload(payload); err != nil {
		return err
	}
	frame := make([]byte, 0, len(payload)+3)
	frame = append(frame, VT)
	frame = append(frame, payload...)
	frame = append(frame, FS, CR)
	_, err := fw.w.Write(frame)
	return err
}

func isASCIILetter(b byte) bool {
	return b >= 'A' && b <= 'Z'
}
