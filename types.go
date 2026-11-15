package netsec

import "fmt"

// PageSize is the fixed IOMMU page size used by this package.
const PageSize uint64 = 4096

// TenantID identifies the security tenant that owns physical pages.
type TenantID uint64

// DeviceID identifies a DMA-capable device. It is supplied by the device and
// is never trusted to select a physical page or domain.
type DeviceID uint32

// DomainID identifies an independent per-device IOMMU domain.
type DomainID uint64

// Generation identifies a version of a device's translations.
type Generation uint64

// PhysPage is a physical page-frame number. The byte address represented by
// page p and offset o is p*PageSize+o.
type PhysPage uint64

// AccPerm is a read/write permission mask. It is used both for mapping
// permissions and for access requested by a transfer.
type AccPerm uint8

const (
	// Read permits reads from the mapped page.
	Read AccPerm = 1 << iota
	// Write permits writes to the mapped page.
	Write
)

// Permissions is an alias for AccPerm.
type Permissions = AccPerm

// Access is an alias for AccPerm.
type Access = AccPerm

func (a AccPerm) valid() bool {
	return a != 0 && a&^(Read|Write) == 0
}

// Allows reports whether p allows every operation in want.
func (p AccPerm) Allows(want AccPerm) bool {
	return want != 0 && want&^(Read|Write) == 0 && p&want == want
}

func (a AccPerm) String() string {
	switch a {
	case Read:
		return "read"
	case Write:
		return "write"
	case Read | Write:
		return "read|write"
	default:
		return fmt.Sprintf("access(%d)", uint8(a))
	}
}

// Segment is one absolute IOVA range in a scatter-gather list.
type Segment struct {
	IOVA   uint64
	Length uint64
}

// TransferRequest is untrusted device input.
//
// IOVA and Length describe the complete request window. If SG is empty, the
// window itself is transferred. If SG is non-empty, every Segment must be an
// absolute range inside the window, the segment lengths must add up to Length,
// and segments are processed in list order. Validation covers the entire list
// before any access callback is invoked.
type TransferRequest struct {
	Device DeviceID
	IOVA   uint64
	Length uint64
	Access AccPerm
	SG     []Segment
}

// AccessOp describes one page-bounded piece of a validated transfer.
type AccessOp struct {
	Device     DeviceID
	Domain     DomainID
	Generation Generation
	IOVA       uint64
	PhysPage   PhysPage
	PhysOffset uint64
	Length     uint64
	Access     AccPerm
}

// AccessFunc is the memory callback invoked after the whole request has been
// validated and all of its mappings have been leased. A callback may block;
// revocation of the affected mappings waits for it to return.
type AccessFunc func(AccessOp) error

// TransferResult summarizes the callbacks completed by a transfer.
type TransferResult struct {
	Chunks int
	Bytes  uint64
}

// MappingInfo is a snapshot of one active translation.
type MappingInfo struct {
	Device     DeviceID
	Tenant     TenantID
	Domain     DomainID
	Generation Generation
	IOVA       uint64
	PhysPage   PhysPage
	Access     AccPerm
}

// DeviceInfo is a snapshot of a device's control-plane identity.
type DeviceInfo struct {
	Device     DeviceID
	Tenant     TenantID
	Domain     DomainID
	Generation Generation
}

// DeviceStats reports active translations and outstanding page leases.
//
// Leases counts page chunks that are still held across callbacks, including
// leases on translations retired by Remap. ActiveMappings counts only current
// translations.
type DeviceStats struct {
	ActiveMappings int
	Leases         int
}
