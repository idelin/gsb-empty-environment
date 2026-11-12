package luks2

import (
	"fmt"
	"math"
	"sort"
)

// ValidateAreas verifies that every active keyslot area lies inside the
// keyslots region [2*headerSize, earliestSegmentOffset) and that no two areas
// overlap. Retained keyslots have no area and are skipped.
func ValidateAreas(header *Header, headerSize uint64) error {
	if header == nil {
		return fmt.Errorf("%w: nil header", ErrMalformedMetadata)
	}
	if headerSize < MinHeaderSize {
		return fmt.Errorf("%w: header size %d below minimum %d", ErrBadHeader, headerSize, MinHeaderSize)
	}
	if headerSize > math.MaxUint64/2 {
		return fmt.Errorf("%w: header size overflow", ErrBadHeader)
	}
	lower := 2 * headerSize
	upper, err := keyslotsUpperBound(header)
	if err != nil {
		return err
	}
	if upper <= lower {
		return fmt.Errorf("%w: keyslots area is empty", ErrAreaOutOfBounds)
	}

	type interval struct {
		id         string
		start, end uint64
	}
	var intervals []interval
	for _, id := range sortedKeys(header.Keyslots) {
		keyslot := header.Keyslots[id]
		if keyslot.Type == TypeRetained {
			continue
		}
		if keyslot.Area == nil {
			return fmt.Errorf("%w: keyslot %s has no area", ErrMalformedMetadata, id)
		}
		start := uint64(keyslot.Area.Offset)
		size := uint64(keyslot.Area.Size)
		if size == 0 {
			return fmt.Errorf("%w: keyslot %s has a zero-size area", ErrMalformedMetadata, id)
		}
		end := start + size
		if end < start {
			return fmt.Errorf("%w: keyslot %s area overflows the address space", ErrAreaOutOfBounds, id)
		}
		if start < lower || end > upper {
			return fmt.Errorf("%w: keyslot %s area [%d,%d) outside keyslots area [%d,%d)",
				ErrAreaOutOfBounds, id, start, end, lower, upper)
		}
		intervals = append(intervals, interval{id: id, start: start, end: end})
	}

	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].id < intervals[j].id
	})
	for i := 1; i < len(intervals); i++ {
		if intervals[i].start < intervals[i-1].end {
			return fmt.Errorf("%w: keyslot %s area overlaps keyslot %s",
				ErrAreaOverlap, intervals[i].id, intervals[i-1].id)
		}
	}
	return nil
}

// keyslotsUpperBound returns the earliest active segment offset: keyslot
// areas must not reach into the user-data region.
func keyslotsUpperBound(header *Header) (uint64, error) {
	if len(header.Segments) == 0 {
		return 0, fmt.Errorf("%w: no segments define the data boundary", ErrMalformedMetadata)
	}
	var upper uint64 = math.MaxUint64
	found := false
	for _, id := range sortedKeys(header.Segments) {
		segment := header.Segments[id]
		if segment.Type == TypeRetained {
			continue
		}
		offset := uint64(segment.Offset)
		if !found || offset < upper {
			upper = offset
		}
		found = true
	}
	if !found {
		return 0, fmt.Errorf("%w: no active segments define the data boundary", ErrMalformedMetadata)
	}
	return upper, nil
}

// ValidateBindings verifies digest references in both directions: every
// keyslot and segment named by an active digest must exist and be active, and
// every active keyslot and segment must be named by at least one digest.
func ValidateBindings(header *Header) error {
	if header == nil {
		return fmt.Errorf("%w: nil header", ErrMalformedMetadata)
	}
	if len(header.Keyslots) == 0 {
		return fmt.Errorf("%w: metadata contains no keyslots", ErrMalformedMetadata)
	}
	if len(header.Digests) == 0 {
		return fmt.Errorf("%w: metadata contains no digests", ErrMalformedMetadata)
	}
	if len(header.Segments) == 0 {
		return fmt.Errorf("%w: metadata contains no segments", ErrMalformedMetadata)
	}

	referencedKeyslots := make(map[string]bool)
	referencedSegments := make(map[string]bool)

	for _, id := range sortedKeys(header.Digests) {
		digest := header.Digests[id]
		if digest.Type == TypeRetained {
			continue
		}
		if len(digest.Keyslots) == 0 || len(digest.Segments) == 0 {
			return fmt.Errorf("%w: digest %s must reference keyslots and segments", ErrMalformedMetadata, id)
		}
		if err := checkDigestRefs(digest.Keyslots, "keyslot", id, header.Keyslots, referencedKeyslots); err != nil {
			return err
		}
		if err := checkDigestRefs(digest.Segments, "segment", id, header.Segments, referencedSegments); err != nil {
			return err
		}
	}

	for _, id := range sortedKeys(header.Keyslots) {
		if header.Keyslots[id].Type == TypeRetained {
			continue
		}
		if !referencedKeyslots[id] {
			return fmt.Errorf("%w: active keyslot %s is not bound to any digest", ErrBindingInconsistent, id)
		}
	}
	for _, id := range sortedKeys(header.Segments) {
		if header.Segments[id].Type == TypeRetained {
			continue
		}
		if !referencedSegments[id] {
			return fmt.Errorf("%w: active segment %s is not bound to any digest", ErrBindingInconsistent, id)
		}
	}
	return nil
}

func checkDigestRefs[V any](refs []string, kind, digestID string,
	targets map[string]V, referenced map[string]bool,
) error {
	seen := make(map[string]bool)
	for _, ref := range refs {
		target, ok := targets[ref]
		if !ok {
			return fmt.Errorf("%w: digest %s references missing %s %s",
				ErrDanglingReference, digestID, kind, ref)
		}
		if retained, _ := isRetained(target); retained {
			return fmt.Errorf("%w: digest %s references retained %s %s",
				ErrDanglingReference, digestID, kind, ref)
		}
		if seen[ref] {
			return fmt.Errorf("%w: digest %s lists %s %s more than once",
				ErrBindingInconsistent, digestID, kind, ref)
		}
		seen[ref] = true
		referenced[ref] = true
	}
	return nil
}

func isRetained[V any](target V) (bool, error) {
	switch v := any(target).(type) {
	case Keyslot:
		return v.Type == TypeRetained, nil
	case Segment:
		return v.Type == TypeRetained, nil
	default:
		return false, fmt.Errorf("%w: unsupported binding target", ErrMalformedMetadata)
	}
}

// ValidateKDF rejects active keyslots whose PBKDF cost factors exceed the
// caller-supplied limits. A zero limit disables that particular check.
func ValidateKDF(header *Header, limits KDFLimits) error {
	if header == nil {
		return fmt.Errorf("%w: nil header", ErrMalformedMetadata)
	}
	for _, id := range sortedKeys(header.Keyslots) {
		keyslot := header.Keyslots[id]
		if keyslot.Type == TypeRetained {
			continue
		}
		if keyslot.KDF == nil {
			return fmt.Errorf("%w: keyslot %s has no KDF parameters", ErrMalformedMetadata, id)
		}
		kdf := keyslot.KDF
		if kdf.Type == "" {
			return fmt.Errorf("%w: keyslot %s KDF has no type", ErrMalformedMetadata, id)
		}
		checks := []struct {
			name  string
			value uint64
			limit uint64
		}{
			{"time", uint64(kdf.Time), limits.MaxTime},
			{"memory", uint64(kdf.Memory), limits.MaxMemory},
			{"cpus", uint64(kdf.CPUs), limits.MaxCPUs},
			{"iterations", uint64(kdf.Iterations), limits.MaxIterations},
		}
		for _, check := range checks {
			if check.limit != 0 && check.value > check.limit {
				return fmt.Errorf("%w: keyslot %s KDF %s=%d exceeds limit %d",
					ErrKDFLimit, id, check.name, check.value, check.limit)
			}
		}
	}
	return nil
}

// Validate runs all JSON-level checks. The binary checksum is validated
// separately via VerifyChecksum because it operates on the raw header bytes.
func Validate(header *Header, headerSize uint64, limits KDFLimits) error {
	if err := ValidateAreas(header, headerSize); err != nil {
		return err
	}
	if err := ValidateBindings(header); err != nil {
		return err
	}
	if err := ValidateKDF(header, limits); err != nil {
		return err
	}
	return nil
}
