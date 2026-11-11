package pagetable

// physInterval is a half-open physical address range covered by mappings.
type physInterval struct {
	start uint64
	end   uint64
}

// physicalCoverage returns the sorted, merged set of physical intervals
// referenced by leaf entries. A 2 MiB leaf and the 512 four KiB PTEs it
// splits into produce identical coverage even though their frame-address
// sets are represented differently.
func physicalCoverage(maps []Mapping) []physInterval {
	iv := make([]physInterval, 0, len(maps))
	for _, m := range maps {
		iv = append(iv, physInterval{start: m.Phys, end: m.Phys + m.Size})
	}
	for i := 1; i < len(iv); i++ {
		for j := i; j > 0 && iv[j-1].start > iv[j].start; j-- {
			iv[j-1], iv[j] = iv[j], iv[j-1]
		}
	}
	merged := make([]physInterval, 0, len(iv))
	for _, cur := range iv {
		if n := len(merged); n > 0 && cur.start <= merged[n-1].end {
			if cur.end > merged[n-1].end {
				merged[n-1].end = cur.end
			}
			continue
		}
		merged = append(merged, cur)
	}
	return merged
}

func intervalsEqual(a, b []physInterval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
