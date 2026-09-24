package baselinepatch

import (
	"fmt"
	"sort"
)

// Diff computes an atomic Patch that transforms baseline into target.
//
// Field changes are represented per key: an added key is OpSet without
// OldExists; a changed key OpSet carrying the old value; a removed key
// OpDelete carrying the removed value. Arrays are compared by LCS and edited
// with a single OpSplice per changed array. Every edit records its exact old
// content, so Inverse needs no extra information.
//
// Both roots must be objects; nil inputs are rejected (Diff never assumes an
// empty object baseline).
func Diff(baseline, target map[string]any) (*Patch, error) {
	if baseline == nil {
		return nil, fmt.Errorf("%w: baseline", ErrMissingBaseline)
	}
	if target == nil {
		return nil, fmt.Errorf("%w: target", ErrMissingBaseline)
	}
	if err := validateValue(baseline); err != nil {
		return nil, err
	}
	if err := validateValue(target); err != nil {
		return nil, err
	}

	var ops []Op
	if err := diffValue(baseline, target, nil, &ops); err != nil {
		return nil, err
	}

	bd, err := Digest(baseline)
	if err != nil {
		return nil, err
	}
	td, err := Digest(target)
	if err != nil {
		return nil, err
	}
	// Validate ordering convention: field ops are emitted in sorted key order
	// and array splices after their containing object's own keys, so distinct
	// paths never interfere and the same inputs always yield the same patch.
	p := &Patch{BaselineDigest: bd, TargetDigest: td, Ops: ops}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func diffValue(a, b any, path []string, ops *[]Op) error {
	if am, aok := a.(map[string]any); aok {
		if bm, bok := b.(map[string]any); bok {
			return diffObject(am, bm, path, ops)
		}
	}
	if aa, aok := a.([]any); aok {
		if ba, bok := b.([]any); bok {
			return diffArray(aa, ba, path, ops)
		}
	}
	eq, err := equalValues(a, b)
	if err != nil {
		return err
	}
	if !eq && len(path) > 0 {
		oldCopy, err := cloneValue(a)
		if err != nil {
			return err
		}
		newCopy, err := cloneValue(b)
		if err != nil {
			return err
		}
		*ops = append(*ops, Op{
			Kind: OpSet, Path: clonePath(path),
			OldExists: true, Old: oldCopy, New: []any{newCopy},
		})
	}
	return nil
}

func diffObject(a, b map[string]any, path []string, ops *[]Op) error {
	keys := map[string]struct{}{}
	for k := range a {
		keys[k] = struct{}{}
	}
	for k := range b {
		keys[k] = struct{}{}
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		childPath := append(clonePath(path), k)
		av, aok := a[k]
		bv, bok := b[k]
		switch {
		case aok && !bok:
			oldCopy, err := cloneValue(av)
			if err != nil {
				return err
			}
			*ops = append(*ops, Op{
				Kind: OpDelete, Path: childPath, Old: oldCopy,
			})
		case !aok && bok:
			newCopy, err := cloneValue(bv)
			if err != nil {
				return err
			}
			*ops = append(*ops, Op{
				Kind: OpSet, Path: childPath, New: []any{newCopy},
			})
		case aok && bok:
			if err := diffValue(av, bv, childPath, ops); err != nil {
				return err
			}
		}
	}
	return nil
}

// diffArray compares two arrays by LCS. Runs between matched anchors become one
// OpSplice. Splices are emitted from the highest index to the lowest, because
// forward Apply performs them in order; descending order keeps earlier
// indices valid regardless of length changes. Removed elements are recorded
// exactly, making the splice self-verifying and invertible.
func diffArray(a, b []any, path []string, ops *[]Op) error {
	n, m := len(a), len(b)
	// LCS is O(n*m). Beyond the cell budget, fall back to replacing the whole
	// array with one splice — linear time, still exact and invertible.
	if uint64(n)*uint64(m) > maxLCSCells {
		return replaceArray(a, b, path, ops)
	}
	// LCS length table.
	tab := make([][]int, n+1)
	for i := range tab {
		tab[i] = make([]int, m+1)
	}
	eqCache := make([][]bool, n)
	for i := range eqCache {
		eqCache[i] = make([]bool, m)
		for j := range eqCache[i] {
			eq, err := equalValues(a[i], b[j])
			if err != nil {
				return err
			}
			eqCache[i][j] = eq
		}
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if eqCache[i][j] {
				tab[i][j] = tab[i+1][j+1] + 1
			} else if tab[i+1][j] >= tab[i][j+1] {
				tab[i][j] = tab[i+1][j]
			} else {
				tab[i][j] = tab[i][j+1]
			}
		}
	}

	// Walk the LCS, collecting edit regions. Each gap between two matched
	// anchors becomes one splice; (startI,startJ) is the beginning of the
	// current gap.
	type region struct {
		index    int
		removed  []any
		inserted []any
	}
	var regions []region
	emit := func(startI, startJ, endI, endJ int) error {
		if startI == endI && startJ == endJ {
			return nil
		}
		removed := make([]any, 0, endI-startI)
		for x := startI; x < endI; x++ {
			c, err := cloneValue(a[x])
			if err != nil {
				return err
			}
			removed = append(removed, c)
		}
		inserted := make([]any, 0, endJ-startJ)
		for y := startJ; y < endJ; y++ {
			c, err := cloneValue(b[y])
			if err != nil {
				return err
			}
			inserted = append(inserted, c)
		}
		regions = append(regions, region{
			index: startI, removed: removed, inserted: inserted,
		})
		return nil
	}
	i, j := 0, 0
	startI, startJ := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && eqCache[i][j]:
			if err := emit(startI, startJ, i, j); err != nil {
				return err
			}
			i++
			j++
			startI, startJ = i, j
		case j < m && (i == n || tab[i+1][j] < tab[i][j+1]):
			j++
		case i < n:
			i++
		default:
			j++
		}
	}
	if err := emit(startI, startJ, n, m); err != nil {
		return err
	}

	// Descending index order for forward application.
	for k := len(regions) - 1; k >= 0; k-- {
		rg := regions[k]
		*ops = append(*ops, Op{
			Kind:        OpSplice,
			Path:        clonePath(path),
			Index:       rg.index,
			DeleteCount: len(rg.removed),
			New:         rg.inserted,
			Removed:     rg.removed,
		})
	}
	return nil
}

// replaceArray emits one splice that swaps the entire array contents.
func replaceArray(a, b []any, path []string, ops *[]Op) error {
	removed := make([]any, len(a))
	for i := range a {
		c, err := cloneValue(a[i])
		if err != nil {
			return err
		}
		removed[i] = c
	}
	inserted := make([]any, len(b))
	for i := range b {
		c, err := cloneValue(b[i])
		if err != nil {
			return err
		}
		inserted[i] = c
	}
	*ops = append(*ops, Op{
		Kind:        OpSplice,
		Path:        clonePath(path),
		Index:       0,
		DeleteCount: len(removed),
		New:         inserted,
		Removed:     removed,
	})
	return nil
}

func clonePath(p []string) []string {
	out := make([]string, len(p))
	copy(out, p)
	return out
}
