// Package netsec implements a requester-qualified MSI/MSI-X interrupt
// remapping unit for a Go virtualization host.
//
// A remapping entry is keyed by (requesterID, deviceGeneration, tableIndex),
// never by vector alone. Install requests are authorized against the device's
// owning domain and validated against the domain's live vCPU generations and
// vector range. The DomainID passed to the low-level Install method is an
// authenticated host value; tenant-facing code should use InstallAs with a
// *Domain capability. Only Fixed delivery is accepted.
//
// Updates and revocations are fail-closed: the affected entry is first made
// non-deliverable, all in-flight delivery receipts are drained, and only then
// is a new target published. Reset and re-tenancy advance the device
// generation and revoke old entries, so old-generation messages cannot match.
// Re-tenancy also returns a new *Device handle and revokes the old one, so a
// previous tenant cannot keep injecting into or resetting the transferred
// device. Interrupt delivery revalidates the domain and vCPU generation
// immediately before the injection callback.
//
// The trust boundary is explicit. A tenant receives a *Device handle and may
// choose table indices and vectors through it. When an explicit capability is
// desired, Remapper.NewDomain issues a *Domain handle and Device.InstallAs
// verifies it (a handle for another domain is rejected regardless of any
// numeric ID known to the caller). A tenant must never receive the *Remapper
// itself: Remapper.AttachDevice, Remapper.Reassign, and the raw
// Remapper.Deliver ingress are host/root-complex operations, and the
// requester ID stamped by Device.SendInterrupt is host-assigned.
//
// All exported Remapper and Device methods are safe for concurrent use.
// Delivery callbacks run without internal locks but must not call control
// operations on the same Remapper, because a control operation may be waiting
// for the callback's receipt.
package netsec
