package iflow

import "fmt"

// Level identifies one secrecy level in a finite, explicitly declared lattice.
type Level int

// Lattice is a finite lattice over secrecy levels. The partial order is given
// by its covering relation; the join (least upper bound) table is precomputed
// and construction fails if any pair of levels lacks a unique join, i.e. the
// declared order is not actually a lattice.
type Lattice struct {
	names  []string
	leq    [][]bool
	join   [][]int
	bottom int
}

// NewLattice builds a lattice from level names and covering pairs (lo, hi)
// meaning lo < hi with nothing in between. The relation is transitively
// closed and checked for antisymmetry and unique joins.
func NewLattice(names []string, covers [][2]string) (*Lattice, error) {
	n := len(names)
	if n == 0 {
		return nil, fmt.Errorf("lattice needs at least one level")
	}
	idx := make(map[string]int, n)
	for i, nm := range names {
		if _, dup := idx[nm]; dup {
			return nil, fmt.Errorf("duplicate level %q", nm)
		}
		idx[nm] = i
	}
	leq := make([][]bool, n)
	for i := range leq {
		leq[i] = make([]bool, n)
		leq[i][i] = true
	}
	for _, c := range covers {
		lo, ok1 := idx[c[0]]
		hi, ok2 := idx[c[1]]
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("cover (%q, %q) mentions unknown level", c[0], c[1])
		}
		leq[lo][hi] = true
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if leq[i][k] && leq[k][j] {
					leq[i][j] = true
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i != j && leq[i][j] && leq[j][i] {
				return nil, fmt.Errorf("levels %q and %q are mutually ordered: not a partial order", names[i], names[j])
			}
		}
	}
	join := make([][]int, n)
	for i := range join {
		join[i] = make([]int, n)
	}
	for a := 0; a < n; a++ {
		for b := 0; b < n; b++ {
			best, count := -1, 0
			for c := 0; c < n; c++ {
				if !leq[a][c] || !leq[b][c] {
					continue
				}
				minimal := true
				for d := 0; d < n; d++ {
					if d != c && leq[a][d] && leq[b][d] && leq[d][c] {
						minimal = false
						break
					}
				}
				if minimal {
					count++
					best = c
				}
			}
			if count != 1 {
				return nil, fmt.Errorf("levels %q and %q have no unique least upper bound: not a lattice", names[a], names[b])
			}
			join[a][b] = best
		}
	}
	bottom := -1
	for i := 0; i < n; i++ {
		all := true
		for j := 0; j < n; j++ {
			if !leq[i][j] {
				all = false
				break
			}
		}
		if all {
			bottom = i
			break
		}
	}
	if bottom < 0 {
		return nil, fmt.Errorf("lattice has no bottom element")
	}
	return &Lattice{names: names, leq: leq, join: join, bottom: bottom}, nil
}

// LevelOf resolves a level name to its Level.
func (l *Lattice) LevelOf(name string) (Level, error) {
	for i, nm := range l.names {
		if nm == name {
			return Level(i), nil
		}
	}
	return 0, fmt.Errorf("unknown level %q", name)
}

// Name returns the declared name of a level.
func (l *Lattice) Name(lv Level) string { return l.names[int(lv)] }

// Leq reports whether a <= b in the lattice order.
func (l *Lattice) Leq(a, b Level) bool { return l.leq[int(a)][int(b)] }

// Join returns the least upper bound of a and b.
func (l *Lattice) Join(a, b Level) Level { return Level(l.join[int(a)][int(b)]) }

// Bottom returns the least level of the lattice.
func (l *Lattice) Bottom() Level { return Level(l.bottom) }
