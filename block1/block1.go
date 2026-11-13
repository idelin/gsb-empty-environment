// Package block1 implements a memory-bounded reassembler for CoAP Block1
// request-body transfers (RFC 7959).
//
// The reassembler is safe against clients that advertise huge block numbers
// or body sizes to force server-side memory allocation: state is created only
// for a transfer that starts at block NUM 0, blocks are absorbed only in
// strict order, every in-flight body is capped at 64 KiB, and each peer may
// hold at most four incomplete transfers.
package block1

import (
	"errors"
	"fmt"
)

// SZX is the block size exponent of a CoAP Block1 option.
type SZX uint8

// Valid Block1 size exponents. The value 7 is reserved by RFC 7959 and is
// rejected, so only 16..1024 byte blocks are accepted.
const (
	SZX16   SZX = 0
	SZX32   SZX = 1
	SZX64   SZX = 2
	SZX128  SZX = 3
	SZX256  SZX = 4
	SZX512  SZX = 5
	SZX1024 SZX = 6
)

// Limits enforced by Reassembler.
const (
	MaxBodySize          = 64 * 1024
	MaxIncompletePerPeer = 4
)

// Sentinel errors returned in Result.Err.
var (
	ErrInvalidSZX           = errors.New("block1: invalid SZX, only 16..1024 byte blocks are supported")
	ErrNUMTooLarge          = errors.New("block1: block number exceeds the 20-bit Block option range")
	ErrPayloadExceedsBlock  = errors.New("block1: payload larger than declared block size")
	ErrShortBlock           = errors.New("block1: non-final block must fill the block size exactly")
	ErrOutOfOrder           = errors.New("block1: block number out of sequence")
	ErrSize1TooLarge        = errors.New("block1: Size1 exceeds 64 KiB")
	ErrExceedsSize1         = errors.New("block1: payload exceeds declared Size1")
	ErrSize1Changed         = errors.New("block1: Size1 changed mid-transfer")
	ErrSizeMismatch         = errors.New("block1: reassembled body length does not match Size1")
	ErrTooManyTransfers     = errors.New("block1: peer has too many incomplete transfers")
	ErrETagChanged          = errors.New("block1: ETag changed mid-transfer")
	ErrContentFormatChanged = errors.New("block1: Content-Format changed mid-transfer")
	ErrSZXChanged           = errors.New("block1: block size SZX changed mid-transfer")
)

// Size returns the payload block size in bytes for the exponent.
func (s SZX) Size() (int, error) {
	if s > SZX1024 {
		return 0, ErrInvalidSZX
	}
	return 16 << s, nil
}

// EncodeBlock builds the integer value of a Block1 option from its fields.
func EncodeBlock(num uint32, more bool, szx SZX) (uint32, error) {
	if szx > SZX1024 {
		return 0, ErrInvalidSZX
	}
	if num > 0xFFFFF { // NUM is 20 bits wide in a 3-byte option
		return 0, ErrNUMTooLarge
	}
	value := num << 4
	if more {
		value |= 1 << 3
	}
	return value | uint32(szx), nil
}

// DecodeBlock parses the integer value of a Block1 option into its fields.
func DecodeBlock(value uint32) (num uint32, more bool, szx SZX) {
	return value >> 4, value&0x08 != 0, SZX(value & 0x07)
}

// Code is a CoAP response code encoded as class*32 + detail, e.g. 4.08 == 136.
type Code uint16

const (
	CodeContinue              Code = (2 << 5) | 31 // 2.31 Continue
	CodeChanged               Code = (2 << 5) | 4  // 2.04 Changed
	CodeBadRequest            Code = (4 << 5)      // 4.00 Bad Request
	CodeEntityIncomplete      Code = (4 << 5) | 8  // 4.08 Request Entity Incomplete
	CodeConflict              Code = (4 << 5) | 9  // 4.09 Conflict
	CodeRequestEntityTooLarge Code = (4 << 5) | 13 // 4.13 Request Entity Too Large
	CodeServiceUnavailable    Code = (5 << 5) | 3  // 5.03 Service Unavailable
)

// String renders the code in CoAP "class.detail" notation, e.g. "4.08".
func (c Code) String() string {
	return fmt.Sprintf("%d.%02d", c>>5, c&0x1f)
}

// Fragment is one Block1 request received from a peer.
type Fragment struct {
	Peer             string // stable peer identifier, e.g. "192.0.2.1:5683"
	Token            []byte // CoAP token identifying the block-wise exchange
	NUM              uint32 // block number from the Block1 option
	More             bool   // M bit from the Block1 option
	SZX              SZX    // block size exponent from the Block1 option
	Payload          []byte // block payload
	HasSize1         bool   // whether the Size1 option was present
	Size1            uint32 // declared total body size
	HasETag          bool   // whether the ETag option was present
	ETag             []byte
	HasContentFormat bool // whether the Content-Format option was present
	ContentFormat    uint16
}

// Status classifies the outcome of processing a Fragment.
type Status uint8

const (
	StatusRejected Status = iota
	StatusContinue
	StatusComplete
)

// Result tells the caller how to answer the peer. On a 4.08 or 2.31 answer,
// WantNUM carries the block number the server expects next and should be
// echoed in the response Block1 option.
type Result struct {
	Status  Status
	Code    Code
	WantNUM uint32
	Body    []byte // reassembled body, populated only when Status == StatusComplete
	Err     error  // rejection reason, populated only when Status == StatusRejected
}
