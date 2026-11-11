// Package safezip reads individual members of a ZIP archive held on object
// storage through ranged ReadAt calls, without downloading the whole object.
//
// Security model:
//
//   - Every member's central directory record is rebound to its local file
//     header; name, compression method and payload data range must match.
//   - ZIP64 extra fields and data descriptors are parsed explicitly and
//     cross-checked against the central directory.
//   - All claimed regions (local headers and payloads) must be disjoint and
//     must stay clear of the central directory.
//   - Directory entries must carry no payload; declared lengths are bounded
//     by the object size and caller-supplied Limits.
//   - Decompressed bytes are only handed to caches or upper layers after the
//     full member is consumed and its CRC-32 matches, so a failed read can
//     never pollute a cache.
package safezip

import (
	"encoding/binary"
	"path"
	"sort"
	"strings"
)

// Limits caps untrusted length claims. A zero value disables the matching
// check; DefaultLimits provides conservative bounds.
type Limits struct {
	MaxMember     int64 // maximum uncompressed member size
	MaxCompressed int64 // maximum compressed payload size
	MaxName       int   // maximum member name length in bytes
	MaxMetadata   int64 // maximum central directory size
}

// DefaultLimits bounds a single member to 1 GiB (uncompressed), a compressed
// payload to 512 MiB, names to 1 KiB and the central directory to 16 MiB.
func DefaultLimits() Limits {
	return Limits{
		MaxMember:     1 << 30,
		MaxCompressed: 1 << 29,
		MaxName:       1 << 10,
		MaxMetadata:   1 << 24,
	}
}

// Config identifies an archive. ArchiveID namespaces cache entries so members
// from different objects can never alias each other.
type Config struct {
	ArchiveID string
	Limits    Limits
}

// Member is a verified central directory entry. Its fields describe the
// payload region exactly as it was bound to the local file header.
type Member struct {
	Name           string
	Method         uint16
	CRC32          uint32
	CompressedSize int64
	Uncompressed   int64
	DataOffset     int64
	DataDescriptor bool

	zip64      bool
	headerEnd  int64
	payloadEnd int64
	regionEnd  int64
}

// Archive is a parsed, fully validated ZIP archive. It is safe for
// concurrent use; member reads issue independent ranged requests.
type Archive struct {
	cfg     Config
	store   Store
	size    int64
	members map[string]*Member
	order   []string
}

// Open reads the central directory of the object behind st and validates
// every entry before returning. Failures return one of the package sentinel
// errors, possibly wrapped with context.
func Open(st Store, cfg Config) (*Archive, error) {
	size := st.Size()
	if size < eocdFixedSize {
		return nil, ErrFormat
	}
	info, err := readEOCD(st, size)
	if err != nil {
		return nil, err
	}
	if info.entries < 0 {
		return nil, ErrFormat
	}
	entries, err := parseCentralDirectory(st, info, cfg.Limits)
	if err != nil {
		return nil, err
	}
	if int64(len(entries)) != info.entries {
		return nil, ErrFormat
	}

	a := &Archive{
		cfg:     cfg,
		store:   st,
		size:    size,
		members: make(map[string]*Member, len(entries)),
		order:   make([]string, 0, len(entries)),
	}

	regions := make([]region, 0, len(entries)*2)
	for _, ce := range entries {
		if err := validateName(ce.name, cfg.Limits); err != nil {
			return nil, err
		}
		isDir := strings.HasSuffix(ce.name, "/") || ce.extAttr&msdosDirectoryAttr != 0
		if isDir {
			// A genuine directory marker is empty; a "directory" with bytes
			// is a pseudo-directory entry and must not reach the cache.
			if ce.compSize != 0 || ce.uncompSize != 0 || ce.crc != 0 {
				return nil, ErrPseudoDirectory
			}
			continue
		}
		if ce.method != methodStore && ce.method != methodDeflate {
			return nil, ErrUnsupported
		}
		if cfg.Limits.MaxMember > 0 && ce.uncompSize > cfg.Limits.MaxMember {
			return nil, ErrDeclaredTooLong
		}
		if cfg.Limits.MaxCompressed > 0 && ce.compSize > cfg.Limits.MaxCompressed {
			return nil, ErrDeclaredTooLong
		}
		m, err := a.bindLocalHeader(ce)
		if err != nil {
			return nil, err
		}
		if _, dup := a.members[m.Name]; dup {
			return nil, ErrBinding
		}
		a.members[m.Name] = m
		a.order = append(a.order, m.Name)
		regions = append(regions,
			region{start: ce.localOff, end: m.headerEnd},
			region{start: m.DataOffset, end: m.regionEnd},
		)
	}

	if err := checkRegions(regions, info.cdOffset, size); err != nil {
		return nil, err
	}
	return a, nil
}

func validateName(name string, limits Limits) error {
	if name == "" || name == "/" {
		return ErrFormat
	}
	if limits.MaxName > 0 && len(name) > limits.MaxName {
		return ErrDeclaredTooLong
	}
	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") ||
		strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) ||
		strings.Contains(name, `\`) {
		return ErrFormat
	}
	return nil
}

// bindLocalHeader fetches the local file header at the offset claimed by the
// central directory and requires the two records to agree on name, method
// and the exact payload range.
func (a *Archive) bindLocalHeader(ce *centralEntry) (*Member, error) {
	fixed := make([]byte, localHeaderFixed)
	if err := readFullAt(a.store, ce.localOff, fixed); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(fixed[0:]) != sigLocal {
		return nil, ErrBinding
	}
	localNameLen := int(binary.LittleEndian.Uint16(fixed[26:]))
	localExtraLen := int(binary.LittleEndian.Uint16(fixed[28:]))
	localFlags := binary.LittleEndian.Uint16(fixed[6:])
	localMethod := binary.LittleEndian.Uint16(fixed[8:])

	headerEnd := ce.localOff + int64(localHeaderFixed+localNameLen+localExtraLen)
	if headerEnd > a.size {
		return nil, ErrTruncated
	}
	nameExtra := make([]byte, localNameLen+localExtraLen)
	if err := readFullAt(a.store, ce.localOff+localHeaderFixed, nameExtra); err != nil {
		return nil, err
	}
	localName := string(nameExtra[:localNameLen])
	if localName != ce.name {
		return nil, ErrBinding
	}
	if localMethod != ce.method || localFlags&^flagDataDescriptor != ce.flags&^flagDataDescriptor {
		return nil, ErrBinding
	}

	dataOff := headerEnd
	payloadEnd := dataOff + ce.compSize
	if payloadEnd > a.size || payloadEnd < dataOff {
		return nil, ErrDeclaredTooLong
	}

	// When the local header carries ZIP64 extra fields its size claims must
	// agree with the central directory; the central directory remains the
	// source of truth for the actual range. A local marker without a central
	// marker, or a size disagreement, is a binding failure.
	if localExtraLen > 0 {
		lz, err := parseLocalExtra(nameExtra[localNameLen:])
		if err != nil {
			return nil, err
		}
		if lz != nil {
			if !ce.hasZip64 {
				return nil, ErrBinding
			}
			if lz.hasUncomp && lz.uncomp != ce.uncompSize {
				return nil, ErrBinding
			}
			if lz.hasComp && lz.comp != ce.compSize {
				return nil, ErrBinding
			}
		} else if ce.hasZip64 {
			return nil, ErrBinding
		}
	}

	// With a data descriptor the local size/crc fields are zero by spec;
	// without one they must equal the central directory values.
	hasDescriptor := ce.flags&flagDataDescriptor != 0
	localCRC := binary.LittleEndian.Uint32(fixed[14:])
	localComp := int64(binary.LittleEndian.Uint32(fixed[18:]))
	localUncomp := int64(binary.LittleEndian.Uint32(fixed[22:]))
	if hasDescriptor {
		if localCRC != 0 || localComp != 0 || localUncomp != 0 {
			return nil, ErrBinding
		}
	} else {
		if localCRC != ce.crc || localComp != ce.compSize || localUncomp != ce.uncompSize {
			return nil, ErrBinding
		}
	}

	// The data descriptor lives immediately after the payload. Treat its
	// bytes as part of the claimed member region so overlap checks cannot
	// be bypassed by hiding a second member inside the descriptor area.
	regionEnd := payloadEnd
	if hasDescriptor {
		descLen := int64(12)
		if ce.hasZip64 {
			descLen = 20
		}
		// Four optional leading signature bytes; bounds are checked again
		// at read time. Reserve space for the longest legal form.
		regionEnd = payloadEnd + descLen + 4
		if regionEnd > a.size || regionEnd < payloadEnd {
			return nil, ErrDeclaredTooLong
		}
	}

	return &Member{
		Name:           ce.name,
		Method:         ce.method,
		CRC32:          ce.crc,
		CompressedSize: ce.compSize,
		Uncompressed:   ce.uncompSize,
		DataOffset:     dataOff,
		DataDescriptor: hasDescriptor,
		zip64:          ce.hasZip64,
		headerEnd:      headerEnd,
		payloadEnd:     payloadEnd,
		regionEnd:      regionEnd,
	}, nil
}

// region is an inclusive-start, exclusive-end byte range inside the object.
type region struct{ start, end int64 }

// checkRegions verifies that every claimed header/payload region is disjoint
// and ends before the central directory begins.
func checkRegions(regions []region, cdOffset, size int64) error {
	for _, r := range regions {
		if r.start < 0 || r.end <= r.start || r.end > size {
			return ErrDeclaredTooLong
		}
		if r.end > cdOffset {
			return ErrOverlap
		}
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].start < regions[j].start })
	for i := 1; i < len(regions); i++ {
		if regions[i].start < regions[i-1].end {
			return ErrOverlap
		}
	}
	return nil
}

// Names returns member names in central directory order.
func (a *Archive) Names() []string {
	out := make([]string, len(a.order))
	copy(out, a.order)
	return out
}

// Lookup returns the verified member descriptor for name, or io.EOF-like
// nil when no such member exists.
func (a *Archive) Lookup(name string) (*Member, bool) {
	m, ok := a.members[name]
	return m, ok
}

// cacheKey namespaces members by archive and member name.
func (a *Archive) cacheKey(m *Member) string {
	var b strings.Builder
	b.WriteString(a.cfg.ArchiveID)
	b.WriteByte('|')
	b.WriteString(m.Name)
	return b.String()
}
