package lsm

import "errors"

// ErrCorruption is returned when two segments contain records with the
// same key and sequence number but different content.
var ErrCorruption = errors.New("lsm: corrupt input: same key and sequence with different content")

const maxSeq = ^uint64(0)

// compactKey reduces the versions of a single key to the minimal set that
// preserves reads at every active snapshot and at any future (higher)
// sequence number. versions must share one key and be sorted by Seq desc.
//
// Keep rules:
//  1. The newest version is always kept (future readers need it).
//  2. A version v is kept if some active snapshot s falls in
//     [v.Seq, nextNewerVersion.Seq): v is exactly what s observes.
//  3. The oldest kept version, if it is a tombstone, is dropped only when
//     no lower-level range covers the key. Lower coverage is decided from
//     key ranges alone, never from inspecting lower-level records, so the
//     proof is conservative. Dropping is safe then because nothing older
//     exists anywhere and a tombstone reads identically to "absent".
//
// A tombstone that still has kept older versions beneath it is never
// dropped: removing it would resurrect those versions for future readers.
func (c *Compaction) compactKey(versions []Record) ([]Record, error) {
	dedup := make([]Record, 0, len(versions))
	for _, v := range versions {
		if n := len(dedup); n > 0 && dedup[n-1].Seq == v.Seq {
			if !dedup[n-1].sameContent(v) {
				return nil, ErrCorruption
			}
			continue // identical duplicate, merge silently
		}
		dedup = append(dedup, v)
	}

	kept := make([]Record, 0, len(dedup))
	for i, v := range dedup {
		keep := i == 0
		if !keep {
			newer := maxSeq
			if i > 0 {
				newer = dedup[i-1].Seq
			}
			for _, s := range c.snapshots {
				if v.Seq <= s && s < newer {
					keep = true
					break
				}
			}
		}
		if keep {
			kept = append(kept, v)
		}
	}

	if n := len(kept); n > 0 {
		if last := kept[n-1]; last.Tombstone && !c.lowerCovers(last.Key) {
			kept = kept[:n-1]
		}
	}
	return kept, nil
}
