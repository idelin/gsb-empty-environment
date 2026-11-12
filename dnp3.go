package netsec

import (
	"errors"
	"fmt"
)

// DefaultMaxMessageBytes bounds the amount of data assembled from transport
// fragments. It is intentionally modest: DNP3 application fragments are small
// and the limit prevents a fragmented request from exhausting gateway memory.
const DefaultMaxMessageBytes = 64 * 1024

const (
	transportFIRMask = 0x80
	transportFINMask = 0x40
	transportSeqMask = 0x3f

	applicationFIRMask = 0x80
	applicationFINMask = 0x40
	applicationSeqMask = 0x0f
)

var (
	// ErrIncompleteTransport means that the supplied fragments do not form one
	// complete transport message (the first frame lacks FIR or the last lacks
	// FIN).
	ErrIncompleteTransport = errors.New("netsec: incomplete transport assembly")
	// ErrMalformedTransport means that frame order, frame sequence numbers, or
	// fragment boundaries are inconsistent.
	ErrMalformedTransport = errors.New("netsec: malformed transport fragments")
	// ErrMalformedApplication means that the reassembled bytes are not a
	// complete DNP3 application request.
	ErrMalformedApplication = errors.New("netsec: malformed application request")
)

// FunctionCode is the DNP3 application function code.
//
// This gateway uses the profile required by this package: SELect is 3,
// OPERATE is 4, cold restart is 13, and hot restart is 14. Other codes in the
// 0 through 22 range are treated as known so that policy, rather than a parser
// accident, decides whether they may be used.
type FunctionCode uint8

const (
	FCConfirm              FunctionCode = 0
	FCRead                 FunctionCode = 1
	FCWrite                FunctionCode = 2
	FCSelect               FunctionCode = 3
	FCOperate              FunctionCode = 4
	FCDirectOperate        FunctionCode = 5
	FCDirectOperateNoAck   FunctionCode = 6
	FCImmediateFreeze      FunctionCode = 7
	FCImmediateFreezeNoAck FunctionCode = 8
	FCColdRestart          FunctionCode = 13
	FCHotRestart           FunctionCode = 14
)

const (
	// FunctionColdRestart and FunctionHotRestart are descriptive aliases.
	FunctionColdRestart = FCColdRestart
	FunctionHotRestart  = FCHotRestart
)

var functionNames = map[FunctionCode]string{
	0:  "confirm",
	1:  "read",
	2:  "write",
	3:  "select",
	4:  "operate",
	5:  "direct-operate",
	6:  "direct-operate-no-ack",
	7:  "immediate-freeze",
	8:  "immediate-freeze-no-ack",
	9:  "function-09",
	10: "function-10",
	11: "function-11",
	12: "function-12",
	13: "cold-restart",
	14: "hot-restart",
	15: "function-15",
	16: "function-16",
	17: "function-17",
	18: "function-18",
	19: "function-19",
	20: "function-20",
	21: "function-21",
	22: "function-22",
}

// Known reports whether fc is a function code understood by the gateway.
// Unknown codes are rejected even if a caller mistakenly puts them in an
// allow-list.
func (fc FunctionCode) Known() bool {
	_, ok := functionNames[fc]
	return ok
}

func (fc FunctionCode) String() string {
	if name, ok := functionNames[fc]; ok {
		return name
	}
	return fmt.Sprintf("unknown-function-%d", uint8(fc))
}

// TransportFragment is one DNP3 transport-layer frame without the link-layer
// header. FIR and FIN are taken from the transport control byte. Sequence is
// the six-bit transport sequence number.
type TransportFragment struct {
	FIR      bool
	FIN      bool
	Sequence uint8
	Payload  []byte
}

// ParseTransportFragment decodes a transport frame whose first byte is the
// transport control byte and whose remaining bytes are the payload.
func ParseTransportFragment(raw []byte) (TransportFragment, error) {
	if len(raw) == 0 {
		return TransportFragment{}, fmt.Errorf("%w: empty frame", ErrMalformedTransport)
	}
	header := raw[0]
	payload := make([]byte, len(raw)-1)
	copy(payload, raw[1:])
	return TransportFragment{
		FIR:      header&transportFIRMask != 0,
		FIN:      header&transportFINMask != 0,
		Sequence: header & transportSeqMask,
		Payload:  payload,
	}, nil
}

// Encode returns raw transport-frame bytes, including the transport control
// byte. It is useful to feed a frame back to a transport stack.
func (f TransportFragment) Encode() []byte {
	header := f.Sequence & transportSeqMask
	if f.FIR {
		header |= transportFIRMask
	}
	if f.FIN {
		header |= transportFINMask
	}
	raw := make([]byte, 1+len(f.Payload))
	raw[0] = header
	copy(raw[1:], f.Payload)
	return raw
}

// ReassembleTransport joins transport fragments into one application payload.
//
// The first fragment must have FIR set, the last must have FIN set, no earlier
// fragment may have FIN set, and transport sequence numbers must advance
// (modulo 64) by one. Fragments are never inspected for a function code before
// the complete message has been assembled.
func ReassembleTransport(fragments []TransportFragment, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	if len(fragments) == 0 {
		return nil, fmt.Errorf("%w: no fragments", ErrIncompleteTransport)
	}

	first := fragments[0]
	if !first.FIR {
		return nil, fmt.Errorf("%w: first fragment lacks FIR", ErrIncompleteTransport)
	}
	last := fragments[len(fragments)-1]
	if !last.FIN {
		return nil, fmt.Errorf("%w: last fragment lacks FIN", ErrIncompleteTransport)
	}

	total := 0
	for i, fragment := range fragments {
		if i > 0 {
			previous := fragments[i-1]
			want := (previous.Sequence + 1) & transportSeqMask
			if fragment.Sequence&transportSeqMask != want {
				return nil, fmt.Errorf(
					"%w: fragment %d has sequence %d, want %d",
					ErrMalformedTransport, i, fragment.Sequence&transportSeqMask, want,
				)
			}
			if fragment.FIR {
				return nil, fmt.Errorf(
					"%w: fragment %d starts a second message",
					ErrMalformedTransport, i,
				)
			}
		}
		if i < len(fragments)-1 && fragment.FIN {
			return nil, fmt.Errorf(
				"%w: fragment %d ends the message early",
				ErrMalformedTransport, i,
			)
		}
		if total+len(fragment.Payload) > maxBytes {
			return nil, fmt.Errorf(
				"%w: assembled message exceeds %d bytes",
				ErrMalformedTransport, maxBytes,
			)
		}
		total += len(fragment.Payload)
	}

	assembled := make([]byte, 0, total)
	for _, fragment := range fragments {
		assembled = append(assembled, fragment.Payload...)
	}
	return assembled, nil
}

// ApplicationRequest is the security-relevant header parsed from a complete
// assembled application payload.
type ApplicationRequest struct {
	Function FunctionCode
	Sequence uint8
	Confirm  bool
}

// ParseApplicationRequest validates the DNP3 application control byte and
// extracts the function code and application sequence number. Both FIR and FIN
// must be set: an incomplete application fragment is not admissible.
func ParseApplicationRequest(payload []byte) (ApplicationRequest, error) {
	if len(payload) < 2 {
		return ApplicationRequest{}, fmt.Errorf(
			"%w: application header and function code are required",
			ErrMalformedApplication,
		)
	}
	control := payload[0]
	if control&applicationFIRMask == 0 {
		return ApplicationRequest{}, fmt.Errorf(
			"%w: application fragment lacks FIR", ErrMalformedApplication,
		)
	}
	if control&applicationFINMask == 0 {
		return ApplicationRequest{}, fmt.Errorf(
			"%w: application fragment lacks FIN", ErrMalformedApplication,
		)
	}
	return ApplicationRequest{
		Function: FunctionCode(payload[1]),
		Sequence: control & applicationSeqMask,
		Confirm:  control&0x20 != 0,
	}, nil
}

// EncodeApplicationRequest builds a minimal complete DNP3 application request.
// Extra object bytes can be appended by the caller.
func EncodeApplicationRequest(function FunctionCode, sequence uint8) []byte {
	return []byte{
		applicationFIRMask | applicationFINMask | (sequence & applicationSeqMask),
		byte(function),
	}
}
