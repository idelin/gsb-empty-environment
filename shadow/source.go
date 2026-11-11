package shadow

import "sort"

// Source identifies one bit of an input byte.
type Source struct {
	Byte int
	Bit  uint8
}

// SourceSet is an immutable-by-convention set of input bit sources.
type SourceSet map[Source]struct{}

// Sources builds a source set.
func Sources(sources ...Source) SourceSet {
	result := make(SourceSet, len(sources))
	for _, source := range sources {
		result[source] = struct{}{}
	}
	return result
}

// Contains reports whether source is present.
func (set SourceSet) Contains(source Source) bool {
	_, ok := set[source]
	return ok
}

// Slice returns the sources in a deterministic order.
func (set SourceSet) Slice() []Source {
	result := make([]Source, 0, len(set))
	for source := range set {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Byte != result[j].Byte {
			return result[i].Byte < result[j].Byte
		}
		return result[i].Bit < result[j].Bit
	})
	return result
}

// Bytes returns the distinct input bytes represented by the set.
func (set SourceSet) Bytes() []int {
	seen := make(map[int]struct{})
	for source := range set {
		seen[source.Byte] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for byteIndex := range seen {
		result = append(result, byteIndex)
	}
	sort.Ints(result)
	return result
}

func cloneSources(set SourceSet) SourceSet {
	if len(set) == 0 {
		return nil
	}
	result := make(SourceSet, len(set))
	for source := range set {
		result[source] = struct{}{}
	}
	return result
}

func emptyOrNil(set SourceSet) SourceSet {
	if len(set) == 0 {
		return nil
	}
	return set
}

func unionSources(sets ...SourceSet) SourceSet {
	size := 0
	for _, set := range sets {
		size += len(set)
	}
	if size == 0 {
		return nil
	}
	result := make(SourceSet, size)
	for _, set := range sets {
		for source := range set {
			result[source] = struct{}{}
		}
	}
	return result
}
