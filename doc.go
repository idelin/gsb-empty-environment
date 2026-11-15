// Package netsec provides admission control for UWB DS-TWR ranging results
// reported to a door-access service.
//
// A Manager binds every accepted result to its currently open session and
// enforces anti-replay ordering: the Scrambled Timestamp Sequence (STS)
// index must be strictly increasing within a session, so duplicate or
// rolled-back indices are rejected. Results are also rejected when their
// timestamp falls outside the session clock-skew tolerance, the responder
// reply delay exceeds the declared session limit, the STS quality marker
// failed, or the DS-TWR computation yields a negative distance.
package netsec
