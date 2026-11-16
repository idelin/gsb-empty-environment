// Package netsec provides admission control for UWB DS-TWR ranging results
// presented to a door-access service.
//
// Every admitted RangingResult must be bound to the currently active session,
// carry a strictly increasing STS index within that session, and satisfy the
// ranging quality, latency, timestamp and distance checks. Replayed responses
// from an old session, duplicate or rolled-back STS indices, inflated reply
// delays, failed STS markers, illegal timestamps and negative distances are
// all rejected before a verified time of flight and distance is produced.
package netsec
