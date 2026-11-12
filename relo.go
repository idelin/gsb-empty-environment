package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrInvalidBTF means the caller-supplied type graph is malformed.
	ErrInvalidBTF = errors.New("invalid BTF")
	// ErrUnknownType means a referenced BTF type ID does not exist.
	ErrUnknownType = errors.New("unknown BTF type")
	// ErrInvalidRelocation means a relocation record is incomplete or malformed.
	ErrInvalidRelocation = errors.New("invalid relocation")
	// ErrInvalidAccess means an access string is not a colon-separated list of
	// non-negative decimal member indices.
	ErrInvalidAccess = errors.New("invalid access string")
	// ErrIndexOutOfBounds means an access segment does not index a member of
	// the local or target type.
	ErrIndexOutOfBounds = errors.New("access index out of bounds")
	// ErrFieldNotFound means the path is valid in the local type but a named
	// field is absent from the target type. It is only tolerated by
	// RelocFieldExists.
	ErrFieldNotFound = errors.New("field not found")
	// ErrTypeIncompatible means the target field type is not a safe local
	// replacement.
	ErrTypeIncompatible = errors.New("incompatible field type")
	// ErrAccessOutOfBounds means a member or the final access crosses its
	// containing type.
	ErrAccessOutOfBounds = errors.New("field access out of bounds")
	// ErrAmbiguousCandidates means target candidate types produced different
	// relocation values.
	ErrAmbiguousCandidates = errors.New("inconsistent target candidates")
	// ErrNoTargetCandidate means no target type corresponds to the local type.
	ErrNoTargetCandidate = errors.New("no target candidate type")
	// ErrUnsupportedRelocation means the relocation kind is unknown to this
	// validator.
	ErrUnsupportedRelocation = errors.New("unsupported relocation")
)

// RelocKind identifies the value a CO-RE relocation produces. The package
// validates field relocations, which are the ones that can redirect memory
// accesses.
type RelocKind uint32

const (
	// RelocFieldByteOffset rewrites a field's byte offset.
	RelocFieldByteOffset RelocKind = iota + 1
	// RelocFieldByteSize reports a field's target footprint.
	RelocFieldByteSize
	// RelocFieldExists reports whether the field path exists. A missing target
	// field is legal only for this kind.
	RelocFieldExists
	// RelocFieldSigned reports whether the target field is signed.
	RelocFieldSigned
)

// Aliases with shorter names, kept for callers that treat the constants as
// plain relocation names.
const (
	FieldByteOffset = RelocFieldByteOffset
	FieldByteSize   = RelocFieldByteSize
	FieldExists     = RelocFieldExists
	FieldSigned     = RelocFieldSigned
)

func (k RelocKind) String() string {
	switch k {
	case RelocFieldByteOffset:
		return "field_byte_offset"
	case RelocFieldByteSize:
		return "field_byte_size"
	case RelocFieldExists:
		return "field_exists"
	case RelocFieldSigned:
		return "field_signed"
	default:
		return fmt.Sprintf("reloc_kind(%d)", uint32(k))
	}
}

func (k RelocKind) isFieldKind() bool {
	switch k {
	case RelocFieldByteOffset, RelocFieldByteSize, RelocFieldExists, RelocFieldSigned:
		return true
	default:
		return false
	}
}

// Relocation is a caller-supplied CO-RE relocation record. LocalTypeID
// identifies the type from the program's BTF. Access is the encoded clang
// access path, for example "0:1:2"; each segment is a member index in the
// local type. AccessSize is the width in bytes read or written by the
// instruction whose offset is being rewritten and is required for
// RelocFieldByteOffset.
//
// If TargetTypeIDs is non-empty those IDs are the candidate target types.
// Otherwise candidate types are found in Target by matching the local root
// type's kind and name.
type Relocation struct {
	Kind          RelocKind
	LocalTypeID   uint32
	Access        string
	AccessSize    uint32
	TargetTypeIDs []uint32
}

// Patch is the validated value of one relocation. Offset and BitOffset
// describe the field's position relative to the target root type. Size is the
// field footprint in bytes (bit-field widths are rounded up). Exists and
// Signed are meaningful for the corresponding relocation kinds.
type Patch struct {
	Relocation    int
	Kind          RelocKind
	TargetTypeID  uint32
	TargetTypeIDs []uint32
	Offset        uint32
	BitOffset     uint32
	Size          uint32
	Exists        bool
	Signed        bool
}

// Validate validates every relocation against the caller-supplied local and
// target BTF. It returns one patch per relocation or the first error,
// annotated with the relocation index.
func Validate(local, target *BTF, relos []Relocation) ([]Patch, error) {
	patches := make([]Patch, 0, len(relos))
	for i := range relos {
		patch, err := validateRelocation(local, target, relos[i])
		if err != nil {
			return nil, fmt.Errorf("relocation %d: %w", i, err)
		}
		patch.Relocation = i
		patches = append(patches, patch)
	}
	return patches, nil
}

// ValidateRelocations is an alias for Validate.
func ValidateRelocations(local, target *BTF, relos []Relocation) ([]Patch, error) {
	return Validate(local, target, relos)
}

// ValidateRelocation validates a single relocation. Its Patch has
// Relocation set to zero; callers that need a stable global index should use
// Validate.
func ValidateRelocation(local, target *BTF, relo Relocation) (Patch, error) {
	return validateRelocation(local, target, relo)
}

func validateRelocation(local, target *BTF, relo Relocation) (Patch, error) {
	if local == nil || target == nil {
		return Patch{}, fmt.Errorf("%w: local and target BTF are required", ErrInvalidBTF)
	}
	if !relo.Kind.isFieldKind() {
		return Patch{}, fmt.Errorf("%w: %s", ErrUnsupportedRelocation, relo.Kind)
	}
	segments, err := parseAccess(relo.Access)
	if err != nil {
		return Patch{}, err
	}
	if _, _, err := local.peelModifiers(relo.LocalTypeID); err != nil {
		return Patch{}, err
	}

	candidates, err := findCandidates(local, target, relo)
	if err != nil {
		return Patch{}, err
	}

	results := make([]candidateResult, 0, len(candidates))
	for _, targetID := range candidates {
		res, walkErr := walkPath(local, target, relo.LocalTypeID, targetID, segments)
		if walkErr != nil {
			if errors.Is(walkErr, errFieldMissing) {
				res.exists = false
			} else {
				return Patch{}, fmt.Errorf("target type %d: %w", targetID, walkErr)
			}
		}
		if !res.exists {
			if relo.Kind != RelocFieldExists {
				return Patch{}, fmt.Errorf("target type %d: %w", targetID, ErrFieldNotFound)
			}
			results = append(results, candidateResult{targetTypeID: targetID})
			continue
		}
		if relo.Kind != RelocFieldExists {
			if err := validateResult(local, target, relo, &res); err != nil {
				return Patch{}, fmt.Errorf("target type %d: %w", targetID, err)
			}
		}
		res.targetTypeID = targetID
		results = append(results, candidateFromWalk(res))
	}
	if len(results) == 0 {
		return Patch{}, ErrNoTargetCandidate
	}
	if err := ensureConsistent(relo.Kind, results); err != nil {
		return Patch{}, err
	}

	patch := makePatch(relo.Kind, results[0])
	patch.TargetTypeIDs = append([]uint32(nil), candidates...)
	return patch, nil
}

func parseAccess(access string) ([]uint32, error) {
	if access == "" {
		return nil, fmt.Errorf("%w: field relocation needs a non-empty access string", ErrInvalidRelocation)
	}
	parts := strings.Split(access, ":")
	segments := make([]uint32, 0, len(parts))
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("%w: empty segment at position %d", ErrInvalidAccess, i)
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil, fmt.Errorf("%w: segment %q is not a decimal index", ErrInvalidAccess, part)
			}
		}
		value, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: segment %q: %v", ErrInvalidAccess, part, err)
		}
		segments = append(segments, uint32(value))
	}
	return segments, nil
}

func findCandidates(local, target *BTF, relo Relocation) ([]uint32, error) {
	if len(relo.TargetTypeIDs) != 0 {
		ids := make([]uint32, 0, len(relo.TargetTypeIDs))
		seen := make(map[uint32]bool, len(relo.TargetTypeIDs))
		for _, id := range relo.TargetTypeIDs {
			if _, ok := target.Type(id); !ok {
				return nil, fmt.Errorf("%w: target type ID %d", ErrUnknownType, id)
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return nil, ErrNoTargetCandidate
		}
		return ids, nil
	}

	_, localRoot, err := local.peelModifiers(relo.LocalTypeID)
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0)
	for id := range target.Types {
		if id == 0 {
			continue
		}
		if sameRootIdentity(local, relo.LocalTypeID, target, id, 0) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: %s %q", ErrNoTargetCandidate, localRoot.Kind, localRoot.Name)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func sameRootIdentity(local *BTF, localID uint32, target *BTF, targetID uint32, depth int) bool {
	if depth > 32 {
		return false
	}
	_, lt, err := local.peelModifiers(localID)
	if err != nil {
		return false
	}
	_, tt, err := target.peelModifiers(targetID)
	if err != nil {
		return false
	}
	if lt.Kind == KindPtr || tt.Kind == KindPtr {
		if lt.Kind != KindPtr || tt.Kind != KindPtr {
			return false
		}
		if lt.TypeID == 0 || tt.TypeID == 0 {
			return false
		}
		return sameRootIdentity(local, lt.TypeID, target, tt.TypeID, depth+1)
	}
	if lt.Kind != tt.Kind {
		return false
	}
	switch lt.Kind {
	case KindStruct, KindUnion, KindFwd:
		return lt.Name != "" && lt.Name == tt.Name
	case KindArray:
		if lt.Nelems != tt.Nelems || lt.ElemTypeID == 0 || tt.ElemTypeID == 0 {
			return false
		}
		return sameRootIdentity(local, lt.ElemTypeID, target, tt.ElemTypeID, depth+1)
	default:
		return lt.Name != "" && lt.Name == tt.Name
	}
}

var errFieldMissing = errors.New("field is missing from target")

type walkResult struct {
	exists          bool
	offsetBits      uint64
	localTypeID     uint32
	targetTypeID    uint32
	localFieldBits  uint64
	targetFieldBits uint64
	rootBits        uint64
	signed          bool
	lastLocal       Member
	lastTarget      Member
	hasLastMember   bool
}

func walkPath(local, target *BTF, localRootID, targetRootID uint32, segments []uint32) (walkResult, error) {
	result := walkResult{exists: true}
	localID := localRootID
	targetID := targetRootID
	var rootBits uint64
	haveRoot := false

	for segmentIndex, segment := range segments {
		peeledLocalID, localType, err := local.peelModifiers(localID)
		if err != nil {
			return result, err
		}
		peeledTargetID, targetType, err := target.peelModifiers(targetID)
		if err != nil {
			return result, err
		}
		localID = peeledLocalID
		targetID = peeledTargetID
		if localType.Kind == KindPtr || targetType.Kind == KindPtr {
			return result, fmt.Errorf("segment %d: pointer type cannot be used as an access container", segmentIndex)
		}
		if localType.Kind != targetType.Kind {
			return result, fmt.Errorf("segment %d: %w: local kind %s, target kind %s",
				segmentIndex, ErrTypeIncompatible, localType.Kind, targetType.Kind)
		}
		if !haveRoot {
			rootSize, err := target.SizeOf(targetID)
			if err != nil {
				return result, err
			}
			rootBits = uint64(rootSize) * 8
			haveRoot = true
		}

		switch localType.Kind {
		case KindStruct, KindUnion:
			if segment >= uint32(len(localType.Members)) {
				return result, fmt.Errorf("segment %d: %w: local %s has %d members, got index %d",
					segmentIndex, ErrIndexOutOfBounds, localType.Kind, len(localType.Members), segment)
			}
			localMember := localType.Members[segment]
			if err := checkMemberBounds(local, localType, localMember, segmentIndex, "local"); err != nil {
				return result, err
			}
			targetMember, ok, err := findTargetMember(targetType, localMember.Name, segment)
			if err != nil {
				return result, fmt.Errorf("segment %d: %w", segmentIndex, err)
			}
			if !ok {
				return walkResult{exists: false}, fmt.Errorf("segment %d: %w", segmentIndex, errFieldMissing)
			}
			if err := checkMemberBounds(target, targetType, targetMember, segmentIndex, "target"); err != nil {
				return result, err
			}
			if uint64(targetMember.BitOffset) > ^uint64(0)-result.offsetBits {
				return result, fmt.Errorf("%w: offset overflow", ErrAccessOutOfBounds)
			}
			result.offsetBits += uint64(targetMember.BitOffset)
			result.lastLocal = localMember
			result.lastTarget = targetMember
			result.hasLastMember = true
			localID = localMember.TypeID
			targetID = targetMember.TypeID

		case KindArray:
			if localType.Nelems == 0 {
				return result, fmt.Errorf("segment %d: %w: cannot index a flexible array", segmentIndex, ErrIndexOutOfBounds)
			}
			if segment >= localType.Nelems {
				return result, fmt.Errorf("segment %d: %w: local array has %d elements, got index %d",
					segmentIndex, ErrIndexOutOfBounds, localType.Nelems, segment)
			}
			if targetType.Nelems == 0 || segment >= targetType.Nelems {
				return walkResult{exists: false}, fmt.Errorf("segment %d: %w", segmentIndex, errFieldMissing)
			}
			targetElemSize, err := target.SizeOf(targetType.ElemTypeID)
			if err != nil {
				return result, err
			}
			product := uint64(segment) * uint64(targetElemSize)
			if segment != 0 && (product/uint64(segment) != uint64(targetElemSize) || product > ^uint64(0)/8) {
				return result, fmt.Errorf("%w: array offset overflow", ErrAccessOutOfBounds)
			}
			elementOffset := product * 8
			if elementOffset > ^uint64(0)-result.offsetBits {
				return result, fmt.Errorf("%w: offset overflow", ErrAccessOutOfBounds)
			}
			result.offsetBits += elementOffset
			result.hasLastMember = false
			localID = localType.ElemTypeID
			targetID = targetType.ElemTypeID

		default:
			return result, fmt.Errorf("segment %d: %w: %s is not a compound or array type",
				segmentIndex, ErrInvalidRelocation, localType.Kind)
		}
	}

	finalLocalID, _, err := local.peelModifiers(localID)
	if err != nil {
		return result, err
	}
	finalTargetID, finalTarget, err := target.peelModifiers(targetID)
	if err != nil {
		return result, err
	}
	result.localTypeID = finalLocalID
	result.targetTypeID = finalTargetID
	result.localFieldBits, err = fieldBits(local, finalLocalID, result.lastLocal, result.hasLastMember)
	if err != nil {
		return result, err
	}
	result.targetFieldBits, err = fieldBits(target, finalTargetID, result.lastTarget, result.hasLastMember)
	if err != nil {
		return result, err
	}
	if !haveRoot {
		rootSize, err := target.SizeOf(targetRootID)
		if err != nil {
			return result, err
		}
		rootBits = uint64(rootSize) * 8
	}
	result.rootBits = rootBits
	if finalTarget.Kind == KindInt {
		result.signed = finalTarget.IntEncoding&IntSigned != 0
	} else if finalTarget.Kind == KindEnum || finalTarget.Kind == KindEnum64 {
		result.signed = finalTarget.EnumSigned
	}
	return result, nil
}

func findTargetMember(container Type, name string, localIndex uint32) (Member, bool, error) {
	if name != "" {
		var found *Member
		for i := range container.Members {
			if container.Members[i].Name != name {
				continue
			}
			if found != nil {
				return Member{}, false, fmt.Errorf("%w: duplicate member %q in target %s",
					ErrInvalidBTF, name, container.Kind)
			}
			found = &container.Members[i]
		}
		if found == nil {
			return Member{}, false, nil
		}
		return *found, true, nil
	}

	if localIndex < uint32(len(container.Members)) && container.Members[localIndex].Name == "" {
		return container.Members[localIndex], true, nil
	}
	var found *Member
	for i := range container.Members {
		if container.Members[i].Name != "" {
			continue
		}
		if found != nil {
			return Member{}, false, fmt.Errorf("%w: ambiguous anonymous members in target %s", ErrInvalidBTF, container.Kind)
		}
		found = &container.Members[i]
	}
	if found == nil {
		return Member{}, false, nil
	}
	return *found, true, nil
}

func checkMemberBounds(btf *BTF, container Type, member Member, segment int, side string) error {
	var widthBits uint64
	if member.BitSize != 0 {
		typeSize, err := btf.SizeOf(member.TypeID)
		if err != nil {
			return err
		}
		underlyingBits := uint64(typeSize) * 8
		if uint64(member.BitSize) > underlyingBits {
			return fmt.Errorf("segment %d: %w: %s bit-field width %d exceeds type width %d",
				segment, ErrInvalidBTF, side, member.BitSize, underlyingBits)
		}
		widthBits = uint64(member.BitSize)
	} else {
		typeSize, err := btf.SizeOf(member.TypeID)
		if err != nil {
			return err
		}
		widthBits = uint64(typeSize) * 8
		if widthBits != 0 && member.BitOffset%8 != 0 {
			return fmt.Errorf("segment %d: %w: %s member %q is not byte aligned",
				segment, ErrInvalidBTF, side, member.Name)
		}
	}
	containerBits := uint64(container.Size) * 8
	if uint64(member.BitOffset) > containerBits || widthBits > containerBits-uint64(member.BitOffset) {
		return fmt.Errorf("segment %d: %w: %s member %q spans bits %d..%d of a %d-bit %s",
			segment, ErrAccessOutOfBounds, side, member.Name, member.BitOffset,
			uint64(member.BitOffset)+widthBits, containerBits, container.Kind)
	}
	return nil
}

func fieldBits(btf *BTF, typeID uint32, last Member, hasLast bool) (uint64, error) {
	if hasLast && last.BitSize != 0 {
		return uint64(last.BitSize), nil
	}
	size, err := btf.SizeOf(typeID)
	if err != nil {
		return 0, err
	}
	return uint64(size) * 8, nil
}

func validateResult(local, target *BTF, relo Relocation, res *walkResult) error {
	switch relo.Kind {
	case RelocFieldByteOffset:
		isBitField := res.hasLastMember && (res.lastLocal.BitSize != 0 || res.lastTarget.BitSize != 0)
		if isBitField && res.lastLocal.BitSize != res.lastTarget.BitSize {
			return fmt.Errorf("%w: local bit-field width %d, target bit-field width %d",
				ErrTypeIncompatible, res.lastLocal.BitSize, res.lastTarget.BitSize)
		}
		if err := typesCompatible(local, res.localTypeID, target, res.targetTypeID); err != nil {
			return err
		}
		if relo.AccessSize == 0 {
			return fmt.Errorf("%w: field byte offset requires AccessSize", ErrInvalidRelocation)
		}
		accessBits := uint64(relo.AccessSize) * 8
		if isBitField {
			// A bit-field may be read with a load wider than the field, as long
			// as the byte-aligned load covers the whole field and stays inside
			// the root type.
			localStorageBits, err := local.SizeOf(res.localTypeID)
			if err != nil {
				return err
			}
			targetStorageBits, err := target.SizeOf(res.targetTypeID)
			if err != nil {
				return err
			}
			if accessBits > uint64(localStorageBits)*8 {
				return fmt.Errorf("%w: access width %d bytes exceeds local bit-field storage %d bytes",
					ErrAccessOutOfBounds, relo.AccessSize, localStorageBits)
			}
			if accessBits > uint64(targetStorageBits)*8 {
				return fmt.Errorf("%w: access width %d bytes exceeds target bit-field storage %d bytes",
					ErrAccessOutOfBounds, relo.AccessSize, targetStorageBits)
			}
			loadStart := res.offsetBits &^ 7
			loadEnd := loadStart + accessBits
			fieldEnd := res.offsetBits + res.targetFieldBits
			if loadEnd > res.rootBits {
				return fmt.Errorf("%w: byte offset %d plus access width %d bytes exceeds target root size %d bytes",
					ErrAccessOutOfBounds, loadStart/8, relo.AccessSize, res.rootBits/8)
			}
			if loadEnd < fieldEnd {
				return fmt.Errorf("%w: %d-byte access does not cover a %d-bit target bit-field at bit %d",
					ErrAccessOutOfBounds, relo.AccessSize, res.targetFieldBits, res.offsetBits)
			}
		} else {
			if accessBits > res.localFieldBits {
				return fmt.Errorf("%w: access width %d bytes exceeds local field width %d bits",
					ErrAccessOutOfBounds, relo.AccessSize, res.localFieldBits)
			}
			if accessBits > res.targetFieldBits {
				return fmt.Errorf("%w: access width %d bytes exceeds target field width %d bits",
					ErrAccessOutOfBounds, relo.AccessSize, res.targetFieldBits)
			}
			if res.offsetBits > res.rootBits || accessBits > res.rootBits-res.offsetBits {
				return fmt.Errorf("%w: offset %d bits + access %d bits exceeds target root size %d bits",
					ErrAccessOutOfBounds, res.offsetBits, accessBits, res.rootBits)
			}
		}

	case RelocFieldByteSize:
		if err := sameKind(local, res.localTypeID, target, res.targetTypeID); err != nil {
			return err
		}

	case RelocFieldSigned:
		if err := sameKind(local, res.localTypeID, target, res.targetTypeID); err != nil {
			return err
		}
		_, tt, err := target.peelModifiers(res.targetTypeID)
		if err != nil {
			return err
		}
		switch tt.Kind {
		case KindInt, KindEnum, KindEnum64:
		default:
			return fmt.Errorf("%w: signedness is not defined for %s", ErrTypeIncompatible, tt.Kind)
		}

	case RelocFieldExists:
		// Existence checks intentionally do not require type compatibility:
		// their only result is whether the field name is present.

	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedRelocation, relo.Kind)
	}
	return nil
}

type candidateResult struct {
	targetTypeID    uint32
	exists          bool
	offsetBits      uint64
	localFieldBits  uint64
	targetFieldBits uint64
	rootBits        uint64
	signed          bool
	bitSize         uint32
	hasBitField     bool
}

func ensureConsistent(kind RelocKind, results []candidateResult) error {
	first := results[0]
	for _, result := range results[1:] {
		consistent := result.exists == first.exists
		if consistent && result.exists {
			switch kind {
			case RelocFieldByteOffset:
				consistent = result.offsetBits == first.offsetBits
				if consistent {
					consistent = result.hasBitField == first.hasBitField && result.bitSize == first.bitSize
				}
			case RelocFieldByteSize:
				consistent = result.targetFieldBits == first.targetFieldBits
			case RelocFieldSigned:
				consistent = result.signed == first.signed
			case RelocFieldExists:
				// exists is the only result.
			}
		}
		if !consistent {
			return fmt.Errorf("%w: target types %d and %d disagree",
				ErrAmbiguousCandidates, first.targetTypeID, result.targetTypeID)
		}
	}
	return nil
}

func makePatch(kind RelocKind, result candidateResult) Patch {
	patch := Patch{
		Kind:         kind,
		TargetTypeID: result.targetTypeID,
		Exists:       result.exists,
		Signed:       result.signed,
	}
	if !result.exists {
		return patch
	}
	patch.Offset = uint32(result.offsetBits / 8)
	patch.BitOffset = uint32(result.offsetBits % 8)
	patch.Size = uint32((result.targetFieldBits + 7) / 8)
	return patch
}

func candidateFromWalk(res walkResult) candidateResult {
	result := candidateResult{
		targetTypeID:    res.targetTypeID,
		exists:          res.exists,
		offsetBits:      res.offsetBits,
		localFieldBits:  res.localFieldBits,
		targetFieldBits: res.targetFieldBits,
		rootBits:        res.rootBits,
		signed:          res.signed,
	}
	if res.hasLastMember && res.lastTarget.BitSize != 0 {
		result.hasBitField = true
		result.bitSize = res.lastTarget.BitSize
	}
	return result
}
