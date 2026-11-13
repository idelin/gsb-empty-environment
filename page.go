package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// DomainID identifies an isolation domain (for example a VM or tenant).
type DomainID string

// PageID identifies a virtual page slot.  Slot numbers are reused.  PageRef
// carries the generation that was current when the reference was created, so
// a stale reference can never resolve to a newly allocated page.
type PageID uint64

// PhysicalPageID identifies a physical page slot.  Like PageID, it is reused
// with a generation.
type PhysicalPageID uint64

// Generation invalidates references after address reuse.
type Generation uint64

// TagSet is a set of sensitive tags attached to a page.
type TagSet map[string]struct{}

// NewTagSet builds a tag set from tag names.
func NewTagSet(tags ...string) TagSet {
	set := make(TagSet, len(tags))
	for _, tag := range tags {
		if tag != "" {
			set[tag] = struct{}{}
		}
	}
	return set
}

// TagsFrom is an alias for NewTagSet.
func TagsFrom(tags ...string) TagSet { return NewTagSet(tags...) }

// Has reports whether the set contains tag.
func (s TagSet) Has(tag string) bool {
	if s == nil {
		return false
	}
	_, ok := s[tag]
	return ok
}

// Empty reports whether the set has no tags.
func (s TagSet) Empty() bool { return len(s) == 0 }

// Clone returns an independent copy.  A nil set is returned as an empty set.
func (s TagSet) Clone() TagSet {
	out := make(TagSet, len(s))
	for tag := range s {
		out[tag] = struct{}{}
	}
	return out
}

// Union returns the union of s and other.
func (s TagSet) Union(other TagSet) TagSet {
	out := make(TagSet, len(s)+len(other))
	for tag := range s {
		out[tag] = struct{}{}
	}
	for tag := range other {
		out[tag] = struct{}{}
	}
	return out
}

// Equal reports set equality.
func (s TagSet) Equal(other TagSet) bool {
	if len(s) != len(other) {
		return false
	}
	for tag := range s {
		if _, ok := other[tag]; !ok {
			return false
		}
	}
	return true
}

// Sorted returns the tag names in deterministic order.
func (s TagSet) Sorted() []string {
	out := make([]string, 0, len(s))
	for tag := range s {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// Key returns a deterministic representation suitable for grouping.
func (s TagSet) Key() string { return strings.Join(s.Sorted(), "\x00") }

// PageRef is a generation-checked reference to a virtual page.
type PageRef struct {
	ID         PageID
	Generation Generation
}

func (r PageRef) String() string {
	return fmt.Sprintf("page %d/%d", r.ID, r.Generation)
}

// PageSpec describes a page before it is installed in a memory host.
type PageSpec struct {
	// DomainID is the owning isolation domain.  Domain is an alias used when
	// DomainID is empty.
	DomainID DomainID
	Domain   DomainID

	// Content is copied by the host.
	Content []byte

	// Digest overrides the computed content digest when non-empty.  It is
	// primarily useful for modelling a broken digest or a digest collision.
	Digest Digest

	// Tags and SensitiveTags are both treated as sensitive labels.  The alias
	// exists because the two spellings are natural in test code.
	Tags          []string
	SensitiveTags []string

	// Shareable must be explicitly true before the conservative policy will
	// consider the page for deduplication.
	Shareable bool
}

// DomainValue returns the effective domain of the specification.
func (s PageSpec) DomainValue() DomainID {
	if s.DomainID != "" {
		return s.DomainID
	}
	return s.Domain
}

// TagValues returns the effective set of tags.
func (s PageSpec) TagValues() TagSet {
	set := NewTagSet(s.Tags...)
	for _, tag := range s.SensitiveTags {
		if tag != "" {
			set[tag] = struct{}{}
		}
	}
	return set
}

// Page is a point-in-time snapshot of a virtual page.  The host never returns
// its internal mutable page objects.
type Page struct {
	ID         PageID
	Generation Generation

	DomainID DomainID
	Digest   Digest
	Tags     TagSet

	// SensitiveTags mirrors Tags.  It is populated for callers that prefer the
	// more explicit spelling.
	SensitiveTags TagSet

	Shareable bool

	PhysicalPageID         PhysicalPageID
	PhysicalPageGeneration Generation

	// RefCount is the number of virtual pages currently mapping the physical
	// page.  It is 1 for a private page and greater than 1 for a merged page.
	RefCount int

	// Content is the logical content visible through this virtual page.
	Content []byte
}

// IsSensitive reports whether the page has any sensitive tag.  Both tag
// spellings are considered because callers may build a Page literal directly.
func (p Page) IsSensitive() bool {
	return len(p.Tags) != 0 || len(p.SensitiveTags) != 0
}

// PhysicalPage is a point-in-time snapshot of a physical page.
type PhysicalPage struct {
	ID         PhysicalPageID
	Generation Generation
	Digest     Digest
	RefCount   int
	COWCount   uint64
	Content    []byte
}

// Stats is a snapshot of host counters.
type Stats struct {
	LivePages           int
	LivePhysicalPages   int
	SharedPhysicalPages int
	MergedPages         int
	COWEvents           uint64
	COWFailures         uint64
	PrivateWrites       uint64
	Accesses            uint64
	Merges              uint64
	ReclaimedPages      uint64
}
