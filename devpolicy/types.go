// Package devpolicy implements a symbolic compiler and audit verifier for
// hierarchical cgroup-v2 device policies.
//
// A device is identified by the triple (Type, Major, Minor). Major and minor
// numbers are 12-bit values in [0, MaxDeviceNo]; permissions are the three
// independent bits r (read), w (write) and m (mknod). The m bit is never
// treated as implying r or w: the three bits are stored and combined
// independently everywhere in the package.
//
// Instead of enumerating the 2*4096*4096 device space, the compiler keeps an
// exact partition of the (major, minor) plane into disjoint axis-aligned
// rectangles (see Diagram), every cell carrying a single decision mask.
// All audit and equivalence operations are performed on that symbolic
// partition, so their cost is proportional to the number of rule-induced
// rectangles rather than to the size of the device-number universe.
package devpolicy

// Device-number domain: Linux major/minor numbers exposed by cgroup device
// controllers are 12-bit values, hence the closed range [0, 4095].
const (
	// MaxDeviceNo is the largest legal major or minor number.
	MaxDeviceNo = 4095
)

// DevType selects the device class. The zero value TypeChar is the ordered
// minimum so that the lexicographic ordering required for counter-examples
// (char before block) falls out naturally.
type DevType byte

const (
	// TypeChar is a character device ('c').
	TypeChar DevType = 'c'
	// TypeBlock is a block device ('b').
	TypeBlock DevType = 'b'
	// TypeAll denotes both character and block devices ('a').
	TypeAll DevType = 'a'
)

// Permission bits. They are independent: granting BitM never grants BitR or
// BitW and vice versa.
const (
	// BitR permits read/open for reading.
	BitR uint8 = 1 << iota
	// BitW permits write/open for writing.
	BitW
	// BitM permits mknod (creation of the device node).
	BitM
	// MaskAll is the union of BitR|BitW|BitM.
	MaskAll = BitR | BitW | BitM
)

// RuleEffect marks a rule as granting or stripping permission bits.
type RuleEffect byte

const (
	// EffectAllow ORs the rule permission mask into the running permission set.
	EffectAllow RuleEffect = iota + 1
	// EffectDeny removes the rule permission mask from the running permission set.
	EffectDeny
)

// Rule is one ordered policy statement inside a single cgroup layer.
//
// Major[Min,Max] and Minor[Min,Max] are closed intervals; a '*' wildcard is
// represented by the full interval [0, MaxDeviceNo]. Type TypeAll fans the
// rule out to both the character and block partitions.
type Rule struct {
	// Layer is the nesting depth, 0 being the root cgroup. Layers must be
	// contiguous starting at 0; effective permissions of depth d can never
	// exceed those granted at depth d-1.
	Layer int
	// Order disambiguates rules sharing a layer; smaller values apply first.
	Order int
	// Effect grants (EffectAllow) or strips (EffectDeny) the Perm bits.
	Effect RuleEffect
	// Type is TypeChar, TypeBlock or TypeAll.
	Type DevType
	// MajorMin/MajorMax bound the closed major interval.
	MajorMin uint16
	MajorMax uint16
	// MinorMin/MinorMax bound the closed minor interval.
	MinorMin uint16
	MinorMax uint16
	// Perm is a subset of BitR|BitW|BitM. It must be non-zero.
	Perm uint8
	// Name is an optional identifier used in witness rule chains.
	Name string
}

// validate checks the structural legality of a rule independently of any
// other rule. The layer-specific contiguity requirement is checked when a
// whole Policy is compiled.
func (r Rule) validate() error {
	switch r.Type {
	case TypeChar, TypeBlock, TypeAll:
	default:
		return invalidf("rule %q: unknown device type %q", r.Name, r.Type)
	}
	switch r.Effect {
	case EffectAllow, EffectDeny:
	default:
		return invalidf("rule %q: unknown effect %d", r.Name, r.Effect)
	}
	if r.Perm&^MaskAll != 0 || r.Perm == 0 {
		return invalidf("rule %q: permission mask 0b%b must be a non-empty subset of rwm", r.Name, r.Perm)
	}
	if r.MajorMin > r.MajorMax || r.MinorMin > r.MinorMax {
		return invalidf("rule %q: inverted interval", r.Name)
	}
	if r.MajorMax > MaxDeviceNo || r.MinorMax > MaxDeviceNo {
		return invalidf("rule %q: device number exceeds %d", r.Name, MaxDeviceNo)
	}
	if r.Layer < 0 {
		return invalidf("rule %q: negative layer %d", r.Name, r.Layer)
	}
	return nil
}

// rect returns the rectangle covered by the rule's number intervals.
func (r Rule) rect() Rect {
	return Rect{
		MajMin: r.MajorMin, MajMax: r.MajorMax,
		MinMin: r.MinorMin, MinMax: r.MinorMax,
	}
}

// types returns the concrete device partitions the rule applies to.
func (r Rule) types() []DevType {
	if r.Type == TypeAll {
		return []DevType{TypeChar, TypeBlock}
	}
	return []DevType{r.Type}
}
