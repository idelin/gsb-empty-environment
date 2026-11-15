package netsec

import "fmt"

// DenyReason classifies why the host refused an operation. Reasons are
// recorded in the host audit log and are safe to return to the requesting
// plugin: they never carry another tenant's resource identity.
type DenyReason int

const (
	DenyUnknownHandle DenyReason = iota + 1
	DenyMalformedHandle
	DenyTamperedHandle
	DenyStaleGeneration
	DenyForeignHandle
	DenyOpNotAllowed
	DenyDelegationWidens
	DenyCycleDetected
	DenyDepthExceeded
	DenyResourceClosed
	DenyInstanceUnloaded
	DenyInstanceRetired
)

func (r DenyReason) String() string {
	switch r {
	case DenyUnknownHandle:
		return "unknown handle"
	case DenyMalformedHandle:
		return "malformed handle"
	case DenyTamperedHandle:
		return "handle bytes do not match any minted capability"
	case DenyStaleGeneration:
		return "handle belongs to a previous instance generation"
	case DenyForeignHandle:
		return "handle is not valid for the requesting instance"
	case DenyOpNotAllowed:
		return "operation not permitted by the effective capability set"
	case DenyDelegationWidens:
		return "delegation may only narrow the effective operations"
	case DenyCycleDetected:
		return "call chain contains a cycle"
	case DenyDepthExceeded:
		return "call chain exceeds the maximum depth"
	case DenyResourceClosed:
		return "resource is closed"
	case DenyInstanceUnloaded:
		return "plugin instance is unloaded"
	case DenyInstanceRetired:
		return "plugin instance generation space is exhausted"
	default:
		return "denied"
	}
}

// Error is a host refusal. It deliberately omits resource names and other
// tenants' identities; Requester is the denied instance itself.
type Error struct {
	Reason    DenyReason
	Requester InstanceID
}

func (e *Error) Error() string {
	return fmt.Sprintf("netsec: access denied: %s", e.Reason)
}
