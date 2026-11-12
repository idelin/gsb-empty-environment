// Package sctpaddr implements authenticated address management for a
// userspace SCTP endpoint.
//
// It is intended to be called from the packet dispatch path of a userspace
// SCTP stack. Every inbound datagram is first screened by a verification-tag
// check (only INIT is exempt), ASCONF chunks require an RFC 4895 AUTH chunk
// whose HMAC is recomputed over a fixed scope, the ASCONF serial must be the
// exact next expected value, newly added addresses are reachability-confirmed
// with a HEARTBEAT nonce before they can become primary, and destructive
// operations on the last address or the transport's source address are
// rejected per RFC 6525.
package sctpaddr
