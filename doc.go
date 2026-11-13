// Package netsec models the two-write doubly-linked-list unlink primitive
// used by a custom allocator.
//
// Memory is fixed at 64 32-bit words. Block headers are 4-word aligned; word
// header+0 is the prev index and word header+1 is the next index. Replay
// executes one attacker-selected (prev, next) pair under one of three precheck
// policies:
//
//	A: all read/write addresses must be in 0..63.
//	B: A plus next.prev and prev.next must both point at the current header.
//	C: B plus both neighbours must be registered free block headers.
//
// Every precheck completes before the first write. The unlink then performs
// m[next] = prev followed by m[prev+1] = next, so the second write wins when
// the addresses alias. Search enumerates explicitly allowed pairs in ascending
// numeric order and reports the first accepted unlink that writes a protected
// word to the host's exact attack value.
package netsec
