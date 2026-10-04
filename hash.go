package antientropy

import "encoding/binary"

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// KeyHash maps a key into the fixed uint64 placement space [0, 2^64).
func KeyHash(key string) uint64 {
	return fnv1a64([]byte(key))
}

func fnv1a64(b []byte) uint64 {
	return fnv1a64Add(fnvOffset64, b)
}

func fnv1a64Add(h uint64, b []byte) uint64 {
	for _, c := range b {
		h ^= uint64(c)
		h *= fnvPrime64
	}
	return h
}

func uint64Bytes(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

// splitmix64 finalizer: avalanches a hash so XOR-aggregated digests mix well.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// combineDigests deterministically merges two child digests (left, right).
func combineDigests(left, right uint64) uint64 {
	return splitmix64(left ^ splitmix64(right+0x9e3779b97f4a7c15))
}
