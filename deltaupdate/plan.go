package delta

import "crypto/sha256"

// Op is one step of the update plan: where the block goes in the new
// file, and whether its bytes are reused from the old file or must be
// downloaded.
type Op struct {
	Index     int   `json:"index"`
	Offset    int64 `json:"offset"`
	Length    int   `json:"length"`
	Reuse     bool  `json:"reuse"`
	OldOffset int64 `json:"old_offset,omitempty"`
}

// Plan pairs the manifest with the per-block reuse/download decision.
type Plan struct {
	Manifest *Manifest `json:"manifest"`
	Ops      []Op      `json:"ops"`
}

// BuildPlan scans the old file with a rolling weak checksum and confirms
// candidates with the strong digest, so a block is reused only when its
// content provably matches — regardless of where it sits in the old file.
// Blocks that moved or appear several times in the new file all resolve
// to the same verified old-file offset. A short trailing block is matched
// against the tail of the old file.
func BuildPlan(m *Manifest, old []byte) *Plan {
	weakMap := make(map[uint32][][32]byte)
	for _, b := range m.Blocks {
		if b.Length == m.BlockSize {
			weakMap[b.Weak] = append(weakMap[b.Weak], b.Strong)
		}
	}

	found := make(map[[32]byte]int64) // strong digest -> offset in old file
	n := m.BlockSize
	if n > 0 && len(old) >= n {
		w := weakSum(old[:n])
		for i := 0; ; i++ {
			if cands, ok := weakMap[w]; ok {
				s := sha256.Sum256(old[i : i+n])
				for _, c := range cands {
					if c == s {
						if _, seen := found[s]; !seen {
							found[s] = int64(i)
						}
					}
				}
			}
			if i+n >= len(old) {
				break
			}
			w = roll(w, old[i], old[i+n], n)
		}
	}

	// Short blocks (only possible as the last block) cannot be found by a
	// full-width rolling window; try the old file's tail instead.
	for _, b := range m.Blocks {
		if b.Length < m.BlockSize && len(old) >= b.Length {
			off := int64(len(old) - b.Length)
			if sha256.Sum256(old[off:]) == b.Strong {
				if _, seen := found[b.Strong]; !seen {
					found[b.Strong] = off
				}
			}
		}
	}

	p := &Plan{Manifest: m}
	for _, b := range m.Blocks {
		op := Op{Index: b.Index, Offset: b.Offset, Length: b.Length}
		if off, ok := found[b.Strong]; ok {
			op.Reuse = true
			op.OldOffset = off
		}
		p.Ops = append(p.Ops, op)
	}
	return p
}
