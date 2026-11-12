package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

// TFTP opcodes (RFC 1350 and RFC 2347).
const (
	OpcodeRRQ   uint16 = 1
	OpcodeWRQ   uint16 = 2
	OpcodeDATA  uint16 = 3
	OpcodeACK   uint16 = 4
	OpcodeERROR uint16 = 5
	OpcodeOACK  uint16 = 6
)

// TFTP error codes (RFC 1350 and RFC 2347).
type ErrorCode uint16

const (
	ErrorNotDefined        ErrorCode = 0
	ErrorFileNotFound      ErrorCode = 1
	ErrorAccessViolation   ErrorCode = 2
	ErrorDiskFull          ErrorCode = 3
	ErrorIllegalOperation  ErrorCode = 4
	ErrorUnknownTID        ErrorCode = 5
	ErrorFileExists        ErrorCode = 6
	ErrorNoSuchUser        ErrorCode = 7
	ErrorOptionNegotiation ErrorCode = 8
)

// Option is a TFTP option name/value pair.
type Option struct {
	Name  string
	Value string
}

// RequestError describes a request that was refused with a TFTP ERROR packet.
// Err, when present, is the underlying path or filesystem error.
type RequestError struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *RequestError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *RequestError) Unwrap() error { return e.Err }

// ErrorPacket encodes a TFTP ERROR packet.
func ErrorPacket(code ErrorCode, message string) []byte {
	if message == "" {
		message = "error"
	}
	msg := []byte(message)
	if i := bytes.IndexByte(msg, 0); i >= 0 {
		msg = msg[:i]
	}
	pkt := make([]byte, 4+len(msg)+1)
	binary.BigEndian.PutUint16(pkt[0:2], OpcodeERROR)
	binary.BigEndian.PutUint16(pkt[2:4], uint16(code))
	copy(pkt[4:], msg)
	return pkt
}

// DataPacket encodes a TFTP DATA packet.
func DataPacket(block uint16, data []byte) []byte {
	pkt := make([]byte, 4+len(data))
	binary.BigEndian.PutUint16(pkt[0:2], OpcodeDATA)
	binary.BigEndian.PutUint16(pkt[2:4], block)
	copy(pkt[4:], data)
	return pkt
}

// ACKPacket encodes a TFTP ACK packet.
func ACKPacket(block uint16) []byte {
	pkt := make([]byte, 4)
	binary.BigEndian.PutUint16(pkt[0:2], OpcodeACK)
	binary.BigEndian.PutUint16(pkt[2:4], block)
	return pkt
}

// OACKPacket encodes a TFTP option acknowledgement packet.
func OACKPacket(options []Option) []byte {
	pkt := make([]byte, 2)
	binary.BigEndian.PutUint16(pkt[0:2], OpcodeOACK)
	for _, opt := range options {
		name := strings.ReplaceAll(opt.Name, "\x00", "")
		value := strings.ReplaceAll(opt.Value, "\x00", "")
		pkt = append(pkt, name...)
		pkt = append(pkt, 0)
		pkt = append(pkt, value...)
		pkt = append(pkt, 0)
	}
	return pkt
}

// RequestPacket encodes an RRQ or WRQ packet. It is primarily useful to
// clients and tests.
func RequestPacket(opcode uint16, filename, mode string, options ...Option) []byte {
	pkt := make([]byte, 2)
	binary.BigEndian.PutUint16(pkt[0:2], opcode)
	pkt = append(pkt, filename...)
	pkt = append(pkt, 0)
	pkt = append(pkt, mode...)
	pkt = append(pkt, 0)
	for _, opt := range options {
		pkt = append(pkt, opt.Name...)
		pkt = append(pkt, 0)
		pkt = append(pkt, opt.Value...)
		pkt = append(pkt, 0)
	}
	return pkt
}

type request struct {
	opcode   uint16
	filename string
	mode     string
	options  []Option
}

func parseRequest(packet []byte) (*request, *RequestError) {
	if len(packet) < 4 {
		return nil, malformed("request packet is too short")
	}
	opcode := binary.BigEndian.Uint16(packet[0:2])
	if opcode != OpcodeRRQ && opcode != OpcodeWRQ {
		return nil, malformed("packet is not an RRQ or WRQ")
	}
	fields, ok := zeroTerminatedFields(packet[2:])
	if !ok || len(fields) < 2 {
		return nil, malformed("request packet is not NUL terminated")
	}
	if fields[0] == "" {
		return nil, &RequestError{Code: ErrorAccessViolation, Message: "empty filename"}
	}
	if fields[1] == "" {
		return nil, malformed("empty transfer mode")
	}
	if (len(fields)-2)%2 != 0 {
		return nil, malformed("option list has an odd number of fields")
	}
	opts := make([]Option, 0, (len(fields)-2)/2)
	for i := 2; i < len(fields); i += 2 {
		if fields[i] == "" {
			return nil, malformed("empty option name")
		}
		opts = append(opts, Option{Name: fields[i], Value: fields[i+1]})
	}
	return &request{
		opcode:   opcode,
		filename: fields[0],
		mode:     fields[1],
		options:  opts,
	}, nil
}

func malformed(message string) *RequestError {
	return &RequestError{Code: ErrorIllegalOperation, Message: message}
}

func zeroTerminatedFields(b []byte) ([]string, bool) {
	if len(b) == 0 || b[len(b)-1] != 0 {
		return nil, false
	}
	return strings.Split(string(b[:len(b)-1]), "\x00"), true
}

func parseACK(packet []byte) (uint16, bool) {
	if len(packet) != 4 || binary.BigEndian.Uint16(packet[0:2]) != OpcodeACK {
		return 0, false
	}
	return binary.BigEndian.Uint16(packet[2:4]), true
}

func parseDATA(packet []byte) (uint16, []byte, bool) {
	if len(packet) < 4 || binary.BigEndian.Uint16(packet[0:2]) != OpcodeDATA {
		return 0, nil, false
	}
	return binary.BigEndian.Uint16(packet[2:4]), packet[4:], true
}
