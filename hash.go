package rehash

import "hash/fnv"

// HashFunc maps a key to a 64-bit hash. Inject a custom HashFunc to
// control bucket placement (e.g. force collisions in tests).
type HashFunc[K comparable] func(K) uint64

// StringHash is FNV-1a over the string bytes.
func StringHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// Uint64Hash is the splitmix64 finalizer: a strong avalanche mixer.
func Uint64Hash(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// IntHash hashes an int via Uint64Hash.
func IntHash(i int) uint64 { return Uint64Hash(uint64(i)) }

// Int64Hash hashes an int64 via Uint64Hash.
func Int64Hash(i int64) uint64 { return Uint64Hash(uint64(i)) }

// Uint32Hash hashes a uint32 via Uint64Hash.
func Uint32Hash(x uint32) uint64 { return Uint64Hash(uint64(x)) }

// BytesHash is FNV-1a over b.
func BytesHash(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}
