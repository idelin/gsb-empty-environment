package netsec

import (
	"encoding/binary"
	"math"
)

const (
	// SerialHalfSpace is the largest legal forward distance for a serial
	// update under RFC 1982 serial-number arithmetic.
	SerialHalfSpace uint32 = uint32(math.MaxInt32)
)

// ForwardDistance returns (to - from) modulo 2^32.
func ForwardDistance(from, to uint32) uint32 { return to - from }

// IsForwardSerial reports whether to is a legal forward update from from.
// A distance of zero (a duplicate), a backward distance, and ambiguous
// distances of 2^31 or more are all rejected.
func IsForwardSerial(from, to uint32) bool {
	distance := ForwardDistance(from, to)
	return distance >= 1 && distance <= SerialHalfSpace
}

// NextSerial advances a serial with 32-bit wrap-around.
func NextSerial(serial uint32) uint32 { return serial + 1 }

// PreviousSerial decrements a serial with 32-bit wrap-around.
func PreviousSerial(serial uint32) uint32 { return serial - 1 }

func appendU32(dst []byte, n uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], n)
	return append(dst, buf[:]...)
}

func appendU64(dst []byte, n uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], n)
	return append(dst, buf[:]...)
}

func appendBytes(dst []byte, b []byte) []byte {
	dst = appendU32(dst, uint32(len(b)))
	return append(dst, b...)
}
