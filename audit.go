// Package netsec audits firmware load plans before a loader applies them.
//
// A firmware image is a list of up to MaxSections sections. Each section
// carries a unique ID, a 32-bit target address, raw bytes (stored as-is or
// RLE encoded), an execute flag, a declared identity, and the raw byte
// ranges covered by its signature. Signature authenticity is established
// only through an injected Verifier callback: a byte is trusted only when
// the callback succeeds for its section and the byte's position lies inside
// a signature-covered range. Byte-level equality with previously loaded
// data is never a substitute for trust.
//
// Audit expands every section, rejects malformed plans (address overflow or
// expansion beyond MaxExpansionBytes) before invoking any load callback,
// then simulates the loads in the caller-specified order where later writes
// overwrite earlier ones. Every address in the execute range (the union of
// the expanded addresses of all execute-flagged sections) must finally hold
// a trusted byte authenticated as the caller-specified target identity.
package netsec

import (
	"errors"
	"fmt"
	"sort"
)

const (
	// MaxSections is the maximum number of sections in one image.
	MaxSections = 32
	// MaxExpansionBytes caps the total expanded size of all sections.
	MaxExpansionBytes = 1 << 20 // 1 MiB
)

var (
	ErrTooManySections = errors.New("netsec: image has more than 32 sections")
	ErrDuplicateID     = errors.New("netsec: duplicate section ID")
	ErrRLEOddLength    = errors.New("netsec: RLE section has odd byte count")
	ErrRLEBadCount     = errors.New("netsec: RLE pair count must be between 1 and 255")
	ErrExpansionLimit  = errors.New("netsec: total expansion exceeds 1 MiB")
	ErrAddressOverflow = errors.New("netsec: section address range overflows 32 bits")
)

// ByteRange is a half-open interval [Offset, Offset+Length) over the raw
// (still encoded) bytes of a section.
type ByteRange struct {
	Offset uint32
	Length uint32
}

// Section describes one loadable section of a firmware image.
type Section struct {
	ID       uint16      // unique within the image
	Address  uint32      // 32-bit target load address
	Raw      []byte      // raw bytes; RLE pairs when RLE is set
	Exec     bool        // contributes its expanded addresses to the execute range
	RLE      bool        // Raw holds (count, value) pairs to decode
	Identity string      // declared identity, passed to the Verifier
	Signed   []ByteRange // raw byte intervals covered by the signature
}

// Verifier authenticates a section. It returns the identity the signature
// actually certifies and whether the signature is valid. The declared
// identity is only an input; trust decisions use the returned identity.
type Verifier func(s Section) (authenticatedIdentity string, ok bool)

// LoadEvent reports one section load, emitted in plan order after the plan
// has passed all structural validation.
type LoadEvent struct {
	SectionID uint16
	Address   uint32
	Length    uint32 // expanded length
	Exec      bool
}

// Loader observes section loads in plan order.
type Loader func(e LoadEvent)

// Violation describes the first execute-range address whose final byte is
// untrusted or authenticated as an unexpected identity.
type Violation struct {
	Address     uint32 // lowest offending execute-range address
	LastWriter  uint16 // section that wrote the final byte at Address
	ExecSection uint16 // section whose execute flag brought Address into the execute range
}

func (v *Violation) Error() string {
	return fmt.Sprintf("netsec: execute-range address %#x untrusted or wrong identity (last writer section %d, exec section %d)",
		v.Address, v.LastWriter, v.ExecSection)
}

type expandedSection struct {
	sec     Section
	data    []byte // expanded bytes
	covered []bool // per expanded byte: signature coverage only
}

func coveredAt(ranges []ByteRange, off uint32) bool {
	for _, r := range ranges {
		if uint64(off) >= uint64(r.Offset) && uint64(off) < uint64(r.Offset)+uint64(r.Length) {
			return true
		}
	}
	return false
}

// expand validates a section and decodes it. No callback runs while any
// section may still be rejected.
func expand(s Section) (expandedSection, error) {
	out := expandedSection{sec: s}
	if !s.RLE {
		out.data = s.Raw
		out.covered = make([]bool, len(s.Raw))
		for i := range s.Raw {
			out.covered[i] = coveredAt(s.Signed, uint32(i))
		}
		return out, nil
	}
	if len(s.Raw)%2 != 0 {
		return out, ErrRLEOddLength
	}
	var total uint64
	for i := 0; i < len(s.Raw); i += 2 {
		count := s.Raw[i]
		if count == 0 {
			return out, ErrRLEBadCount
		}
		total += uint64(count)
	}
	if total > MaxExpansionBytes {
		return out, ErrExpansionLimit
	}
	out.data = make([]byte, 0, total)
	out.covered = make([]bool, 0, total)
	for i := 0; i < len(s.Raw); i += 2 {
		count, value := s.Raw[i], s.Raw[i+1]
		// A pair is covered only when both of its raw bytes are signed;
		// otherwise every byte it expands to is untrusted.
		pairCovered := coveredAt(s.Signed, uint32(i)) && coveredAt(s.Signed, uint32(i+1))
		for k := 0; k < int(count); k++ {
			out.data = append(out.data, value)
			out.covered = append(out.covered, pairCovered)
		}
	}
	return out, nil
}

type cell struct {
	trusted  bool
	identity string
	writer   uint16
}

// Audit validates and simulates a firmware load plan. It returns a
// structural error before any Loader callback runs if the plan is malformed
// (too many sections, duplicate IDs, bad RLE, expansion beyond 1 MiB, or
// 32-bit address overflow). Otherwise it verifies each section, replays the
// loads in order, and reports the first execute-range address whose final
// byte is untrusted or not authenticated as targetIdentity. A nil
// *Violation with a nil error means the plan is safe to load.
func Audit(sections []Section, targetIdentity string, verify Verifier, load Loader) (*Violation, error) {
	if len(sections) > MaxSections {
		return nil, ErrTooManySections
	}
	seen := make(map[uint16]struct{}, len(sections))
	expanded := make([]expandedSection, 0, len(sections))
	var totalExpanded uint64
	for _, s := range sections {
		if _, dup := seen[s.ID]; dup {
			return nil, fmt.Errorf("%w: %d", ErrDuplicateID, s.ID)
		}
		seen[s.ID] = struct{}{}
		ex, err := expand(s)
		if err != nil {
			return nil, err
		}
		totalExpanded += uint64(len(ex.data))
		if totalExpanded > MaxExpansionBytes {
			return nil, ErrExpansionLimit
		}
		if uint64(s.Address)+uint64(len(ex.data)) > 1<<32 {
			return nil, fmt.Errorf("%w: section %d at %#x", ErrAddressOverflow, s.ID, s.Address)
		}
		expanded = append(expanded, ex)
	}

	type auth struct {
		identity string
		ok       bool
	}
	verdicts := make([]auth, len(expanded))
	for i, ex := range expanded {
		if verify != nil {
			id, ok := verify(ex.sec)
			verdicts[i] = auth{identity: id, ok: ok}
		}
	}

	mem := make(map[uint32]cell)
	execRange := make(map[uint32]uint16) // address -> first exec section covering it
	for i, ex := range expanded {
		if load != nil {
			load(LoadEvent{
				SectionID: ex.sec.ID,
				Address:   ex.sec.Address,
				Length:    uint32(len(ex.data)),
				Exec:      ex.sec.Exec,
			})
		}
		a := verdicts[i]
		for j := range ex.data {
			addr := ex.sec.Address + uint32(j)
			mem[addr] = cell{
				trusted:  ex.covered[j] && a.ok,
				identity: a.identity,
				writer:   ex.sec.ID,
			}
		}
		if ex.sec.Exec {
			for j := range ex.data {
				addr := ex.sec.Address + uint32(j)
				if _, exists := execRange[addr]; !exists {
					execRange[addr] = ex.sec.ID
				}
			}
		}
	}

	addrs := make([]uint32, 0, len(execRange))
	for addr := range execRange {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	for _, addr := range addrs {
		c := mem[addr]
		if !c.trusted || c.identity != targetIdentity {
			return &Violation{
				Address:     addr,
				LastWriter:  c.writer,
				ExecSection: execRange[addr],
			}, nil
		}
	}
	return nil, nil
}
