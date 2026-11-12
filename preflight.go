package netsec

import (
	"fmt"
	"sort"
)

func validateConfig(config Config) error {
	if config.DeviceSize <= 0 {
		return fmt.Errorf("%w: device size must be positive, got %d", ErrInvalidConfig, config.DeviceSize)
	}
	if config.MaxExpansion <= 0 {
		return fmt.Errorf("%w: expansion budget must be positive, got %d", ErrInvalidConfig, config.MaxExpansion)
	}
	return nil
}

// preflight proves every claim in the manifest before any payload byte is
// decompressed. Nothing in this phase touches staging storage or the device.
func preflight(manifest Manifest, config Config, registry map[Compression]Decompressor) error {
	if len(manifest.Partitions) == 0 {
		return ErrEmptyManifest
	}

	for index := range manifest.Protected {
		protected := manifest.Protected[index]
		if !protected.Valid() {
			return fmt.Errorf("%w: protected[%d] %+v", ErrInvalidRange, index, protected)
		}
		if !protected.Within(config.DeviceSize) {
			return fmt.Errorf("%w: protected[%d] %+v does not fit device of %d bytes",
				ErrRangeOutOfDevice, index, protected, config.DeviceSize)
		}
	}

	names := make(map[string]struct{}, len(manifest.Partitions))
	var totalExpansion int64

	for index := range manifest.Partitions {
		part := manifest.Partitions[index]
		label := part.Name
		if label == "" {
			label = fmt.Sprintf("#%d", index)
		}

		if part.Name == "" {
			return fmt.Errorf("%w: partition[%d] has empty name", ErrInvalidRange, index)
		}
		if _, exists := names[part.Name]; exists {
			return fmt.Errorf("%w: %q", ErrDuplicateName, part.Name)
		}
		names[part.Name] = struct{}{}

		if !part.Dest.Valid() {
			return fmt.Errorf("%w: partition %q destination %+v", ErrInvalidRange, label, part.Dest)
		}
		if !part.Dest.Within(config.DeviceSize) {
			return fmt.Errorf("%w: partition %q destination %+v does not fit device of %d bytes",
				ErrRangeOutOfDevice, label, part.Dest, config.DeviceSize)
		}

		if _, ok := registry[part.Compression]; !ok {
			return fmt.Errorf("%w: partition %q uses %q", ErrUnknownCompression, label, part.Compression)
		}
		if part.Open == nil {
			return fmt.Errorf("%w: partition %q", ErrMissingPayload, label)
		}

		hasher, err := newHasher(part.HashAlgo)
		if err != nil {
			return fmt.Errorf("partition %q: %w", label, err)
		}
		if len(part.ExpectedHash) != hasher.Size() {
			return fmt.Errorf("%w: partition %q: want %d bytes for %s, got %d",
				ErrBadHashLength, label, hasher.Size(), part.HashAlgo, len(part.ExpectedHash))
		}

		for protectedIndex := range manifest.Protected {
			if part.Dest.Overlaps(manifest.Protected[protectedIndex]) {
				return fmt.Errorf("%w: partition %q %+v intersects protected[%d] %+v",
					ErrProtectedOverlap, label, part.Dest, protectedIndex, manifest.Protected[protectedIndex])
			}
		}

		next := totalExpansion + part.Dest.Length
		if next < totalExpansion || next > config.MaxExpansion {
			return fmt.Errorf("%w: partition %q pushes total to %d, budget is %d",
				ErrExpansionBudget, label, next, config.MaxExpansion)
		}
		totalExpansion = next
	}

	// Sortable copy so the pairwise overlap check is O(n log n) rather than
	// quadratic and reports the first conflicting pair deterministically.
	dests := make([]Range, len(manifest.Partitions))
	byName := make([]string, len(manifest.Partitions))
	for index := range manifest.Partitions {
		dests[index] = manifest.Partitions[index].Dest
		byName[index] = manifest.Partitions[index].Name
	}
	order := make([]int, len(dests))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(a, b int) bool {
		return dests[order[a]].Offset < dests[order[b]].Offset
	})
	for index := 1; index < len(order); index++ {
		prev := dests[order[index-1]]
		cur := dests[order[index]]
		if prev.Overlaps(cur) {
			return fmt.Errorf("%w: %q %+v overlaps %q %+v",
				ErrPartitionOverlap, byName[order[index-1]], prev, byName[order[index]], cur)
		}
	}
	return nil
}
