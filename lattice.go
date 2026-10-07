package flowsec

import "fmt"

// Lattice is an explicit finite lattice of secrecy levels.
// Elements are declared by name; the order is given explicitly by a
// covering relation (Hasse diagram edges, low -> high). leq is the
// reflexive-transitive closure, and join/meet tables are precomputed
// and validated so the structure is guaranteed to be a lattice.
type Lattice struct {
	names []string
	leq   [][]bool
	joinT [][]int
	meetT [][]int
}

// NewLattice builds a lattice from element names and covering pairs
// (indexes into names, low -> high). It fails if the resulting order
// is not a lattice (some pair lacks a unique join or meet).
func NewLattice(names []string, covers [][2]int) (*Lattice, error) {
	n := len(names)
	if n == 0 {
		return nil, fmt.Errorf("lattice needs at least one element")
	}
	leq := make([][]bool, n)
	for i := range leq {
		leq[i] = make([]bool, n)
		leq[i][i] = true
	}
	for _, c := range covers {
		a, b := c[0], c[1]
		if a < 0 || a >= n || b < 0 || b >= n {
			return nil, fmt.Errorf("cover (%d,%d) out of range", a, b)
		}
		leq[a][b] = true
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
	l := &Lattice{names: append([]string(nil), names...), leq: leq}
	l.joinT = make([][]int, n)
	l.meetT = make([][]int, n)
	for a := 0; a < n; a++ {
		l.joinT[a] = make([]int, n)
		l.meetT[a] = make([]int, n)
		for b := 0; b < n; b++ {
			j, err := l.bound(a, b, true)
			if err != nil {
				return nil, err
			}
			m, err := l.bound(a, b, false)
			if err != nil {
				return nil, err
			}
			l.joinT[a][b] = j
			l.meetT[a][b] = m
		}
	}
	return l, nil
}

// ChainLattice builds a total order names[0] < names[1] < ... .
func ChainLattice(names ...string) (*Lattice, error) {
	var covers [][2]int
	for i := 0; i+1 < len(names); i++ {
		covers = append(covers, [2]int{i, i + 1})
	}
	return NewLattice(names, covers)
}

func (l *Lattice) bound(a, b int, upper bool) (int, error) {
	n := len(l.names)
	var cand []int
	for c := 0; c < n; c++ {
		if upper {
			if l.leq[a][c] && l.leq[b][c] {
				cand = append(cand, c)
			}
		} else if l.leq[c][a] && l.leq[c][b] {
			cand = append(cand, c)
		}
	}
	best := -1
	for _, c := range cand {
		least := true
		for _, d := range cand {
			// upper: least = below every candidate; lower: above every one.
			if upper && !l.leq[c][d] {
				least = false
				break
			}
			if !upper && !l.leq[d][c] {
				least = false
				break
			}
		}
		if least {
			if best != -1 && best != c {
				return -1, fmt.Errorf("no unique bound for %s,%s: not a lattice", l.names[a], l.names[b])
			}
			best = c
		}
	}
	if best == -1 {
		kind := "join"
		if !upper {
			kind = "meet"
		}
		return -1, fmt.Errorf("no %s for %s,%s: not a lattice", kind, l.names[a], l.names[b])
	}
	return best, nil
}

func (l *Lattice) Leq(a, b int) bool { return l.leq[a][b] }
func (l *Lattice) Join(a, b int) int { return l.joinT[a][b] }
func (l *Lattice) Meet(a, b int) int { return l.meetT[a][b] }
func (l *Lattice) Name(i int) string { return l.names[i] }
func (l *Lattice) Size() int         { return len(l.names) }

// Index returns the element id for a name.
func (l *Lattice) Index(name string) (int, bool) {
	for i, n := range l.names {
		if n == name {
			return i, true
		}
	}
	return -1, false
}
