// Package netsec provides a per-HTTP/3-connection registry for routing
// WebTransport streams and datagrams to the session identified by its
// CONNECT request stream ID.
//
// A Registry must be scoped to a single HTTP/3 connection. It rejects
// CONNECT requests whose Origin is not explicitly allowlisted, buffers
// traffic for sessions that have not been registered yet, reset streams
// that cannot be routed, and never routes traffic to closed sessions.
package netsec
