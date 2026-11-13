// Package netsec recovers four-byte last-round key candidates from paired
// correct/faulty outputs of the fault model used by this exercise.
//
// The recovered value is a four-byte round-key segment, not a complete cipher
// master key. Callers should treat multiple candidates as an unresolved result.
package netsec
