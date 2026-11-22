package lease

import "strconv"

// ConnectionID identifies a QUIC connection. It is its own namespace:
// a ConnectionID and a StreamID with the same integer value are
// different objects and do not interoperate.
type ConnectionID uint64

// StreamID identifies a QUIC request stream within a connection.
type StreamID uint64

// AuthID is the business authorization identifier of a request.
type AuthID uint64

// TenantID identifies the tenant that owns a request.
type TenantID uint64

// Generation numbers successive leases of the same business request.
// Establishment starts at generation 1; every migration increments it.
type Generation uint64

func (id ConnectionID) String() string { return "conn:" + strconv.FormatUint(uint64(id), 10) }
func (id StreamID) String() string     { return "stream:" + strconv.FormatUint(uint64(id), 10) }
func (id AuthID) String() string       { return "auth:" + strconv.FormatUint(uint64(id), 10) }
func (id TenantID) String() string     { return "tenant:" + strconv.FormatUint(uint64(id), 10) }
func (g Generation) String() string    { return "gen:" + strconv.FormatUint(uint64(g), 10) }
