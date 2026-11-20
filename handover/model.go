package handover

import "strings"

// DeviceID is the permanent hardware identity of a device. It survives
// resets and distinguishes distinct devices that may alias the same physical
// memory.
type DeviceID string

// QueueID identifies a queue within a device.
type QueueID string

// BootGen is the device boot generation, incremented by a reset.
type BootGen uint64

// DomainGen is the domain (VM/partition) generation under which the window was
// programmed.
type DomainGen uint64

// PermBits describes read and write access.
type PermBits uint8

const (
	// PermRead grants read visibility into the covered physical pages.
	PermRead PermBits = 1 << iota
	// PermWrite grants write access to the covered physical pages.
	PermWrite
)

// Read reports whether the read bit is set.
func (p PermBits) Read() bool { return p&PermRead != 0 }

// Write reports whether the write bit is set.
func (p PermBits) Write() bool { return p&PermWrite != 0 }

// Identity binds an access permission or its invalidation confirmation to the
// exact (device, boot, domain, queue) tuple. Invalidation confirmations match
// leases on the complete tuple: a queue re-created after reset with the same
// QueueID has a different BootGen and therefore never matches.
type Identity struct {
	Device DeviceID
	Boot   BootGen
	Domain DomainGen
	Queue  QueueID
}

// Validate checks that every component of the identity is populated.
func (id Identity) Validate() error {
	if strings.TrimSpace(string(id.Device)) == "" {
		return errf("identity has empty device id")
	}
	if strings.TrimSpace(string(id.Queue)) == "" {
		return errf("identity for device %q has empty queue id", id.Device)
	}
	return nil
}

// identityOrder imposes a deterministic ordering on identity tuples so that
// credential contents are stable across map iteration.
func identityOrder(a, b Identity) bool {
	if a.Device != b.Device {
		return a.Device < b.Device
	}
	if a.Boot != b.Boot {
		return a.Boot < b.Boot
	}
	if a.Domain != b.Domain {
		return a.Domain < b.Domain
	}
	return a.Queue < b.Queue
}

// Lease is an outstanding in-flight access: device id.Queue (at a specific boot
// and domain generation) holds Read and/or Write permission over the physical
// pages in Pages until it is invalidated.
type Lease struct {
	ID    string
	Owner Identity
	Perm  PermBits
	Pages IntervalSet
}

// Confirmation is a hardware-issued invalidation acknowledgement. It certifies
// that the exact Owner tuple has relinquished the Perm access over Pages.
// Multiple confirmations may jointly cover a lease; different tuples (for
// example across reset generations) never combine.
type Confirmation struct {
	ID    string
	Owner Identity
	Perm  PermBits
	Pages IntervalSet
}

// Segment is one projection of a logical address window onto shared physical
// memory. Logical pages in [LogBase, LogBase+2^N) map as
//
//	physical = PhysBase + perm.Apply(logical - LogBase).
//
// Logical ranges stay contiguous while the bit permutation may scatter them
// into discrete physical pages.
type Segment struct {
	LogBase  uint64
	PhysBase uint64
	Perm     Perm
}

// Width returns the number of logical pages in the segment, 2^Perm.N.
func (s Segment) Width() uint64 { return uint64(1) << s.Perm.N }

// Validate checks alignment, width, permutation and the 48-bit bounds of both
// endpoint ranges.
func (s Segment) Validate() error {
	if err := s.Perm.Validate(); err != nil {
		return err
	}
	width := s.Width()
	if s.LogBase%width != 0 {
		return errf("segment logical base %d not aligned to width %d", s.LogBase, width)
	}
	if s.PhysBase%width != 0 {
		return errf("segment physical base %d not aligned to width %d", s.PhysBase, width)
	}
	logEnd, ok := addU64(s.LogBase, width-1)
	if !ok || logEnd > MaxPage {
		return errf("segment logical range overflows the %d-bit page space", PageBits)
	}
	physEnd, ok := addU64(s.PhysBase, width-1)
	if !ok || physEnd > MaxPage {
		return errf("segment physical range overflows the %d-bit page space", PageBits)
	}
	return nil
}

// logicalImage maps logical pages (relative coordinates are translated by the
// caller) to absolute physical pages.
func (s Segment) logicalImage(local IntervalSet) (IntervalSet, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	limit := s.Width()
	for _, iv := range local {
		if iv.End > limit {
			return nil, errf("local interval [%d,%d) outside segment width %d", iv.Start, iv.End, limit)
		}
	}
	image, err := s.Perm.Image(local)
	if err != nil {
		return nil, err
	}
	return image.Shift(s.PhysBase)
}

// toLocal translates absolute logical pages to segment-relative coordinates.
func (s Segment) toLocal(pages IntervalSet) (IntervalSet, error) {
	if s.LogBase == 0 {
		return cloneSet(pages), nil
	}
	out := make(IntervalSet, 0, len(pages))
	for _, iv := range pages {
		if iv.Start < s.LogBase {
			return nil, errf("interval start %d precedes segment base %d", iv.Start, s.LogBase)
		}
		out = append(out, Interval{iv.Start - s.LogBase, iv.End - s.LogBase})
	}
	return out, nil
}

// localToLogical shifts segment-relative pages back to absolute logical
// coordinates.
func (s Segment) localToLogical(local IntervalSet) (IntervalSet, error) {
	return local.Shift(s.LogBase)
}

// physicalToLocal removes the physical base, producing permutation-domain
// coordinates.
func (s Segment) physicalToLocal(pages IntervalSet) (IntervalSet, error) {
	if s.PhysBase == 0 {
		return cloneSet(pages), nil
	}
	out := make(IntervalSet, 0, len(pages))
	for _, iv := range pages {
		if iv.Start < s.PhysBase {
			return nil, errf("physical interval start %d precedes segment base %d", iv.Start, s.PhysBase)
		}
		out = append(out, Interval{iv.Start - s.PhysBase, iv.End - s.PhysBase})
	}
	return out, nil
}

// Window is an address window programmed for an identity tuple. Segments are
// disjoint in logical coordinates and may project into shared physical space.
type Window struct {
	Owner    Identity
	Segments []Segment
}

// Validate checks the owner, every segment and the disjointness of logical
// and physical coverage.
func (w Window) Validate() error {
	if err := w.Owner.Validate(); err != nil {
		return err
	}
	if len(w.Segments) == 0 {
		return errf("window for device %q has no segments", w.Owner.Device)
	}
	var logCov, physCov []Interval
	for idx, seg := range w.Segments {
		if err := seg.Validate(); err != nil {
			return errf("window segment %d invalid: %w", idx, err)
		}
		width := seg.Width()
		logCov = append(logCov, Interval{seg.LogBase, seg.LogBase + width})
		physCov = append(physCov, Interval{seg.PhysBase, seg.PhysBase + width})
	}
	if hasOverlap(logCov) {
		return errf("window for device %q has overlapping logical segments", w.Owner.Device)
	}
	if hasOverlap(physCov) {
		return errf("window for device %q has overlapping physical segments", w.Owner.Device)
	}
	return nil
}

// hasOverlap reports whether any two half-open intervals genuinely overlap.
// Adjacent intervals (one ending where the next begins) do not overlap.
func hasOverlap(ivs []Interval) bool {
	sorted := append([]Interval(nil), ivs...)
	sortSlice(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Start < sorted[i-1].End {
			return true
		}
	}
	return false
}

// coveringSegment returns the segment whose logical range contains page p.
func (w Window) coveringSegment(p uint64) (Segment, bool) {
	for _, seg := range w.Segments {
		if seg.LogBase <= p && p < seg.LogBase+seg.Width() {
			return seg, true
		}
	}
	return Segment{}, false
}

// Manifest is the window inventory plus every lease and confirmation known to
// the host. It is the full evidence context needed to issue or verify a
// handover credential. The structure itself carries no hardware handles.
type Manifest struct {
	Windows       []Window
	Leases        []Lease
	Confirmations []Confirmation
}

// Validate checks every window, lease and confirmation, including range bounds
// and non-empty identifiers.
func (m *Manifest) Validate() error {
	for idx := range m.Windows {
		if err := m.Windows[idx].Validate(); err != nil {
			return errf("window %d invalid: %w", idx, err)
		}
	}
	for idx, lease := range m.Leases {
		if strings.TrimSpace(lease.ID) == "" {
			return errf("lease %d has empty id", idx)
		}
		if err := lease.Owner.Validate(); err != nil {
			return errf("lease %q invalid owner: %w", lease.ID, err)
		}
		if lease.Perm == 0 {
			return errf("lease %q has no permissions", lease.ID)
		}
		if lease.Perm&^(PermRead|PermWrite) != 0 {
			return errf("lease %q has unknown permission bits", lease.ID)
		}
		if lease.Pages.IsEmpty() {
			return errf("lease %q covers no pages", lease.ID)
		}
		for _, iv := range lease.Pages {
			if err := checkInterval(iv); err != nil {
				return errf("lease %q invalid range: %w", lease.ID, err)
			}
		}
	}
	for idx, conf := range m.Confirmations {
		if strings.TrimSpace(conf.ID) == "" {
			return errf("confirmation %d has empty id", idx)
		}
		if err := conf.Owner.Validate(); err != nil {
			return errf("confirmation %q invalid owner: %w", conf.ID, err)
		}
		if conf.Perm == 0 {
			return errf("confirmation %q has no permissions", conf.ID)
		}
		if conf.Perm&^(PermRead|PermWrite) != 0 {
			return errf("confirmation %q has unknown permission bits", conf.ID)
		}
		if conf.Pages.IsEmpty() {
			return errf("confirmation %q covers no pages", conf.ID)
		}
		for _, iv := range conf.Pages {
			if err := checkInterval(iv); err != nil {
				return errf("confirmation %q invalid range: %w", conf.ID, err)
			}
		}
	}
	return nil
}

// AliasWindows returns the owners of every window whose physical projection
// contains any page in pages, regardless of which window the pages were
// requested through. It is an inspection helper for cross-device aliasing:
// handover safety is still derived from leases, but this shows every tuple that
// can currently observe the physical pages.
func (m *Manifest) AliasWindows(pages IntervalSet) ([]Identity, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	seen := map[Identity]bool{}
	var owners []Identity
	for _, win := range m.Windows {
		for _, seg := range win.Segments {
			segRange := IntervalSet{{seg.PhysBase, seg.PhysBase + seg.Width()}}
			if !Intersect(segRange, pages).IsEmpty() {
				if !seen[win.Owner] {
					seen[win.Owner] = true
					owners = append(owners, win.Owner)
				}
			}
		}
	}
	sortIdentities(owners)
	return owners, nil
}

func sortIdentities(ids []Identity) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && identityOrder(ids[j], ids[j-1]); j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
}
