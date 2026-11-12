// Package netsec defines the binding and ring-processing policy for the
// AF_XDP socket proxy.
//
// A multi-tenant host hands AF_XDP sockets to tenants that may be hostile.
// In zero-copy mode the NIC performs DMA directly into tenant-modifiable
// memory: a tenant can mutate a packet after the XDP program has classified
// it, or program a descriptor whose address points outside the UMEM. The
// checks in this package are meant to be called by the proxy at bind time and
// every time a TX/FILL descriptor or a ring index crosses the trust boundary.
//
// The package contains no cgo or syscall dependencies so the policy can be
// unit-tested on any platform; callers map its results onto the real
// XDP_FLAGS_* constants and ring operations.
package netsec
