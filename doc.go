// Package netsec provides a hash table hardened against hash-collision
// denial-of-service attacks for use by request decoders that accept
// attacker-controlled keys.
//
// Each Table is keyed with an independent random secret drawn from a
// caller-supplied random source; table creation fails if the secret
// cannot be read. Buckets holding more than 8 entries are converted to
// AVL trees ordered by raw key bytes, so worst-case (fully colliding)
// workloads degrade to O(log n) key comparisons per operation instead
// of O(n). All operations are serialized through a single RWMutex,
// which makes concurrent growth, deletion and lookup linearizable. The
// secret never appears in any output.
package netsec
