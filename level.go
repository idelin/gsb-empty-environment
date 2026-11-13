package netsec

import "fmt"

// Level is the amount of authority a simulated client token carries.
// Ordering is intentional: a higher numeric value grants strictly more.
type Level uint8

const (
	// Identification allows the identity to be read, but no resource access.
	Identification Level = iota
	// Impersonation allows access to resources local to the host.
	Impersonation
	// Delegation allows local access and calls to remote resources.
	Delegation
)

func (l Level) Valid() bool {
	return l <= Delegation
}

func (l Level) String() string {
	switch l {
	case Identification:
		return "Identification"
	case Impersonation:
		return "Impersonation"
	case Delegation:
		return "Delegation"
	default:
		return fmt.Sprintf("Level(%d)", uint8(l))
	}
}

// ResourceKind describes where a resource lives. The scope of a token is
// checked against this value on every resource call.
type ResourceKind uint8

const (
	// LocalResource is a host-local resource such as a local file or socket.
	LocalResource ResourceKind = iota
	// RemoteResource is a resource reached through a remote protocol.
	RemoteResource
)

func (k ResourceKind) String() string {
	switch k {
	case LocalResource:
		return "local"
	case RemoteResource:
		return "remote"
	default:
		return fmt.Sprintf("ResourceKind(%d)", uint8(k))
	}
}

func (k ResourceKind) valid() bool {
	return k == LocalResource || k == RemoteResource
}
