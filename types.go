package netsec

import "fmt"

// RequesterID is the PCIe requester identifier (bus/device/function) of a
// device. It is assigned by the trusted host/root complex, not by a tenant.
// The remapper keys every mapping by this value; a tenant can choose the
// table index and message vector it writes, but can never choose the
// requester identity used on the ingress path.
type RequesterID uint16

// MakeRequesterID encodes a PCIe bus/device/function tuple.
func MakeRequesterID(bus, device, function uint8) (RequesterID, error) {
	if device > 31 {
		return 0, fmt.Errorf("netsec: invalid PCIe device %d", device)
	}
	if function > 7 {
		return 0, fmt.Errorf("netsec: invalid PCIe function %d", function)
	}
	return RequesterID(uint16(bus)<<8 | uint16(device)<<3 | uint16(function)), nil
}

// MustRequesterID is MakeRequesterID for host configuration constants.
func MustRequesterID(bus, device, function uint8) RequesterID {
	id, err := MakeRequesterID(bus, device, function)
	if err != nil {
		panic(err)
	}
	return id
}

// Bus returns the PCIe bus number encoded in the requester ID.
func (r RequesterID) Bus() uint8 { return uint8(r >> 8) }

// Device returns the PCIe device number encoded in the requester ID.
func (r RequesterID) Device() uint8 { return uint8(r>>3) & 0x1f }

// Function returns the PCIe function number encoded in the requester ID.
func (r RequesterID) Function() uint8 { return uint8(r) & 0x07 }

// DeviceGeneration identifies one incarnation of a requester. It is advanced
// whenever the device is reset or moved to another domain. A message carrying
// an older generation cannot match entries installed for the current one.
type DeviceGeneration uint64

// DomainID identifies a tenant/domain that owns vCPUs and devices.
type DomainID uint64

// VCPUID identifies a vCPU inside a domain.
type VCPUID uint32

// VCPUGeneration identifies one incarnation of a vCPU. Hot-removal and
// re-addition must advance it.
type VCPUGeneration uint64

// TableIndex is an MSI-X table index. It is chosen by the device owner and is
// part of the remapping key together with requester ID and device generation.
type TableIndex uint16

// Vector is a message-signalled interrupt vector. Valid x86 vectors are
// 0x00..0xff; larger values are rejected at ingress.
type Vector uint16

// MaxVector is the largest architecturally valid x86 interrupt vector.
const MaxVector Vector = 0xff

// Valid reports whether v is an architecturally valid vector.
func (v Vector) Valid() bool { return v <= MaxVector }

// DeliveryMode is the MSI delivery mode. Only fixed delivery is accepted.
type DeliveryMode uint8

const (
	// DeliveryFixed is the only mode permitted by the remapper.
	DeliveryFixed DeliveryMode = iota
	DeliveryLowestPriority
	DeliverySMI
	DeliveryNMI
	DeliveryInit
	DeliveryExtINT
)

func (m DeliveryMode) String() string {
	switch m {
	case DeliveryFixed:
		return "Fixed"
	case DeliveryLowestPriority:
		return "LowestPriority"
	case DeliverySMI:
		return "SMI"
	case DeliveryNMI:
		return "NMI"
	case DeliveryInit:
		return "INIT"
	case DeliveryExtINT:
		return "ExtINT"
	default:
		return fmt.Sprintf("DeliveryMode(%d)", uint8(m))
	}
}

// VectorRange is an inclusive range of vectors a domain may target.
type VectorRange struct {
	Min Vector
	Max Vector
}

// Validate checks the range against the architectural vector limit.
func (r VectorRange) Validate() error {
	if !r.Min.Valid() || !r.Max.Valid() {
		return fail("vector-range", ErrVectorInvalid,
			"range [%#x,%#x] exceeds maximum %#x", r.Min, r.Max, MaxVector)
	}
	if r.Min > r.Max {
		return fail("vector-range", ErrVectorInvalid,
			"minimum %#x is greater than maximum %#x", r.Min, r.Max)
	}
	return nil
}

// Contains reports whether v is inside the inclusive range.
func (r VectorRange) Contains(v Vector) bool {
	return r.Min <= v && v <= r.Max
}

// VCPU is a vCPU and the generation the host currently considers live.
type VCPU struct {
	ID         VCPUID
	Generation VCPUGeneration
}

// DomainConfig describes the initial state of a domain.
type DomainConfig struct {
	Vectors VectorRange
	VCPUs   []VCPU
}

// InstallRequest asks the remapper to install or replace one MSI-X table
// entry. VCPUGeneration must equal the domain's current generation for VCPU.
type InstallRequest struct {
	TableIndex     TableIndex
	VCPU           VCPUID
	VCPUGeneration VCPUGeneration
	Vector         Vector
	DeliveryMode   DeliveryMode
}

// Interrupt is the message presented by the trusted ingress path. RequesterID
// and DeviceGeneration must be obtained from the device/root complex; a tenant
// controls only TableIndex and Vector through the MSI-X table.
type Interrupt struct {
	RequesterID      RequesterID
	DeviceGeneration DeviceGeneration
	TableIndex       TableIndex
	Vector           Vector
}

// Delivery is the validated target handed to the host's injection callback.
type Delivery struct {
	RequesterID      RequesterID
	DeviceGeneration DeviceGeneration
	TableIndex       TableIndex
	DomainID         DomainID
	VCPUID           VCPUID
	VCPUGeneration   VCPUGeneration
	Vector           Vector
	DeliveryMode     DeliveryMode
}

// DeliveryFunc performs the actual vCPU injection. Its return value is the
// interrupt receipt: an update barrier does not complete until every in-flight
// callback for the affected entry has returned.
//
// The callback is invoked without internal locks held and therefore may block.
// It must not call back into control operations on the same Remapper (Install,
// Reset, Reassign, AddVCPU, RemoveVCPU, and so on); doing so can deadlock the
// barrier that is waiting for this receipt.
type DeliveryFunc func(Delivery) error

// Entry is a point-in-time view of one remapping entry.
type Entry struct {
	RequesterID      RequesterID
	DeviceGeneration DeviceGeneration
	TableIndex       TableIndex
	DomainID         DomainID
	VCPUID           VCPUID
	VCPUGeneration   VCPUGeneration
	Vector           Vector
	DeliveryMode     DeliveryMode
	Present          bool
}

type entryKey struct {
	requester  RequesterID
	generation DeviceGeneration
	tableIndex TableIndex
}

type target struct {
	domain         DomainID
	vcpu           VCPUID
	vcpuGeneration VCPUGeneration
	vector         Vector
	mode           DeliveryMode
}
