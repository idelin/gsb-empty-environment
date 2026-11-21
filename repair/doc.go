// Package repair schedules erasure-coded stripe repair when object storage
// settles read traffic per rack.
//
// Each readable shard is a known linear combination, over GF(2^8) with modulus
// polynomial x^8+x^4+x^3+x^2+1 (0x11d), of at most twelve source byte vectors.
// The caller supplies up to twenty-four readable shards with coefficient rows,
// per-read prices and rack identifiers, the first-read fee of each rack, and
// the coefficient rows of the targets to reconstruct. A single shard read
// serves every target simultaneously.
//
// Schedule searches the readable subsets for the minimum total charge, where
// the charge is the sum of per-read prices plus the first-read fee of every
// rack touched by the selection. All prices and fees are non-negative int64
// values. Ties are broken first by the number of selected shards (fewer wins)
// and then by the lexicographically smaller ascending index list.
//
// Full stripe decodability is not required: a target is recovered whenever its
// coefficient row lies in the span of the selected shard rows. If no target can
// all be recovered, Schedule returns a Certificate, i.e. a dual-space column
// vector orthogonal to every readable row but with inner product one against
// one unrecoverable target. CheckCertificate independently validates such a
// witness without trusting the scheduler.
package repair
