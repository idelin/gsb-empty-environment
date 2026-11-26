package sdf

import "errors"

// appendSmall encodes a non-negative integer as two little-endian bytes.
// Edge capacities never exceed the small hardware limit.
func appendSmall(b []byte, v int) []byte {
	return append(b, byte(v&0xff), byte((v>>8)&0xff))
}

// errOverflow reports an integer time computation that would leave int64.
var errOverflow = errors.New("sdf: time arithmetic overflowed int64")

// add64 returns a+b or false when the sum overflows int64.
func add64(a, b int64) (int64, bool) {
	if b > 0 && a > 1<<63-1-b {
		return 0, false
	}
	if b < 0 && a < -1<<63-b {
		return 0, false
	}
	return a + b, true
}
