package netsec

import "errors"

// BindMode selects how an AF_XDP socket is bound to a queue. The numeric
// values match the kernel XDP_COPY / XDP_ZEROCOPY bind flags so callers can
// pass the mode straight through to the bind syscall.
type BindMode uint16

const (
	// ModeCopy forces the kernel to copy packets between the UMEM and the
	// driver buffers. The NIC never touches tenant memory directly.
	ModeCopy BindMode = 1 << 1 // XDP_COPY
	// ModeZeroCopy lets the NIC DMA directly into UMEM frames. It is only
	// permitted for trusted tenants.
	ModeZeroCopy BindMode = 1 << 2 // XDP_ZEROCOPY
)

// String renders the mode for logs and error messages.
func (m BindMode) String() string {
	switch m {
	case ModeCopy:
		return "copy"
	case ModeZeroCopy:
		return "zero-copy"
	default:
		return "unknown"
	}
}

// Tenant describes the principal whose socket is being bound.
type Tenant struct {
	// ID identifies the tenant in audit logs. It must be non-empty.
	ID string
	// Trusted grants the tenant permission to bind zero-copy sockets. An
	// untrusted tenant may only use copy mode.
	Trusted bool
}

// BindRequest is the policy input for one socket bind.
type BindRequest struct {
	Tenant Tenant
	Mode   BindMode
}

var (
	// ErrEmptyTenant is returned when a request carries no tenant identity.
	ErrEmptyTenant = errors.New("netsec: bind request has no tenant id")
	// ErrInvalidBindMode is returned when the mode is neither copy nor
	// zero-copy, including requests that set both or neither flag.
	ErrInvalidBindMode = errors.New("netsec: invalid bind mode")
	// ErrZeroCopyDenied is returned when an untrusted tenant asks for
	// zero-copy. The request is rejected outright; the proxy must not
	// silently fall back to copy mode.
	ErrZeroCopyDenied = errors.New("netsec: zero-copy bind denied for untrusted tenant")
)

// Policy is the stateless entry point used by the proxy. All exported checks
// hang off it so a single value can be threaded through bind and ring code.
type Policy struct{}

// New returns the default policy.
func New() Policy { return Policy{} }

// AuthorizeBind decides whether a socket may be bound the way the tenant
// asked. Copy mode is allowed for every identified tenant. Zero-copy is
// allowed only for trusted tenants and is rejected, never downgraded, for
// untrusted ones.
func (Policy) AuthorizeBind(req BindRequest) error {
	if req.Tenant.ID == "" {
		return ErrEmptyTenant
	}
	switch req.Mode {
	case ModeCopy:
		return nil
	case ModeZeroCopy:
		if req.Tenant.Trusted {
			return nil
		}
		return ErrZeroCopyDenied
	default:
		return ErrInvalidBindMode
	}
}
