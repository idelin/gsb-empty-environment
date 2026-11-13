// Package netsec validates HTTP/2 request pseudo-headers for protocol
// gateways.
//
// A request header block is accepted only when :method, :scheme, :authority,
// and :path each occur at most once, every pseudo-header precedes all regular
// header fields, and :method and :scheme both precede :path.  Requests must
// contain :method, :scheme, and :path; :authority is optional.
package netsec
