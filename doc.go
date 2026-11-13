// Package netsec implements closure escape analysis for a finite SSA graph and
// a small interpreter that verifies dangling stack captures after a frame is
// released and its slots are filled by another tenant.
//
// A program is built with NewBuilder or by constructing Program/Op values
// directly. Analyze returns shortest escape evidence, missing HeapPromote
// paths, merged capture origins, and an unknown verdict when the budget is
// insufficient. VerifyFinding replays an unsafe path with ExecutePath and
// checks that the reported by-ref capture observes attacker-owned slot bytes.
package netsec
