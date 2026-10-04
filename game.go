// Package zerosum solves two-player zero-sum matrix games exactly.
//
// All arithmetic is carried out with math/big.Rat, so the returned game
// value and mixed strategies are exact rational numbers, never floating
// point approximations. Every solution is accompanied by a certificate
// (a pair of bounds) that lets a third party verify optimality without
// trusting the solver.
package zerosum

import (
	"errors"
	"fmt"
	"math/big"
)

// Game is a two-player zero-sum matrix game.
// A[i][j] is the payoff from the column player to the row player
// when the row player chooses row i and the column player column j.
type Game struct {
	M int          // number of rows (row player's pure strategies)
	N int          // number of columns (column player's pure strategies)
	A [][]*big.Rat // M x N payoff matrix, row player perspective
}

// NewGame builds a game from a rational payoff matrix.
// The matrix must be rectangular and non-empty. Entries are copied.
func NewGame(a [][]*big.Rat) (*Game, error) {
	if len(a) == 0 || len(a[0]) == 0 {
		return nil, errors.New("zerosum: payoff matrix must be non-empty")
	}
	n := len(a[0])
	g := &Game{M: len(a), N: n, A: make([][]*big.Rat, len(a))}
	for i, row := range a {
		if len(row) != n {
			return nil, fmt.Errorf("zerosum: row %d has length %d, want %d", i, len(row), n)
		}
		g.A[i] = make([]*big.Rat, n)
		for j, v := range row {
			if v == nil {
				return nil, fmt.Errorf("zerosum: nil entry at (%d,%d)", i, j)
			}
			g.A[i][j] = new(big.Rat).Set(v)
		}
	}
	return g, nil
}

// NewGameFromInts builds a game from an integer payoff matrix.
func NewGameFromInts(a [][]int64) (*Game, error) {
	r := make([][]*big.Rat, len(a))
	for i, row := range a {
		r[i] = make([]*big.Rat, len(row))
		for j, v := range row {
			r[i][j] = big.NewRat(v, 1)
		}
	}
	return NewGame(r)
}

// Certificate is a verifiable proof of optimality.
//
// The row player's mixed strategy x guarantees an expected payoff of at
// least Lower against every pure (hence every mixed) column response:
//
//	Lower = min_j sum_i x_i * A[i][j]
//
// The column player's mixed strategy y caps the expected payoff at Upper
// against every pure (hence every mixed) row response:
//
//	Upper = max_i sum_j A[i][j] * y_j
//
// When Lower == Upper == Value both strategies are exact optima and Value
// is the game value, by the minimax theorem. Anyone can recompute Lower
// and Upper from (A, x, y) alone.
type Certificate struct {
	Value *big.Rat   // claimed game value
	Lower *big.Rat   // payoff the row strategy provably guarantees
	Upper *big.Rat   // payoff the column strategy provably allows at most
	Row   []*big.Rat // row player's mixed strategy x
	Col   []*big.Rat // column player's mixed strategy y
}

// Solution is an exact optimal solution of the game.
type Solution struct {
	Value *big.Rat
	Row   []*big.Rat // optimal mixed strategy of the row player
	Col   []*big.Rat // optimal mixed strategy of the column player
	Cert  Certificate
}

// RowSupport returns the indices of rows played with positive probability.
func (s *Solution) RowSupport() []int { return supportOf(s.Row) }

// ColSupport returns the indices of columns played with positive probability.
func (s *Solution) ColSupport() []int { return supportOf(s.Col) }

func supportOf(p []*big.Rat) []int {
	var s []int
	for i, v := range p {
		if v.Sign() > 0 {
			s = append(s, i)
		}
	}
	return s
}

// Verify checks that (x, y, v) is an exact optimal solution of the game
// and returns its certificate. It reports false if x or y is not a valid
// probability distribution (entries negative or not summing to exactly 1)
// or if the guaranteed bounds do not bracket v, i.e. if the claimed
// solution is not optimal.
func (g *Game) Verify(x, y []*big.Rat, v *big.Rat) (Certificate, bool) {
	cert := Certificate{}
	if len(x) != g.M || len(y) != g.N || v == nil {
		return cert, false
	}
	if !isDistribution(x) || !isDistribution(y) {
		return cert, false
	}
	lower := g.rowGuarantee(x)
	upper := g.colCap(y)
	cert = Certificate{
		Value: new(big.Rat).Set(v),
		Lower: lower,
		Upper: upper,
		Row:   copyVec(x),
		Col:   copyVec(y),
	}
	// lower <= upper always holds for distributions, so lower >= v >= upper
	// forces lower == v == upper: both strategies are exact optima.
	ok := lower.Cmp(v) >= 0 && upper.Cmp(v) <= 0
	return cert, ok
}

// isDistribution reports whether p is non-negative and sums to exactly 1.
func isDistribution(p []*big.Rat) bool {
	sum := new(big.Rat)
	for _, v := range p {
		if v == nil || v.Sign() < 0 {
			return false
		}
		sum.Add(sum, v)
	}
	return sum.Cmp(big.NewRat(1, 1)) == 0
}

// rowGuarantee returns min_j sum_i x_i * A[i][j], the worst-case expected
// payoff of the row mixed strategy x.
func (g *Game) rowGuarantee(x []*big.Rat) *big.Rat {
	best := new(big.Rat)
	for j := 0; j < g.N; j++ {
		payoff := new(big.Rat)
		for i := 0; i < g.M; i++ {
			payoff.Add(payoff, new(big.Rat).Mul(x[i], g.A[i][j]))
		}
		if j == 0 || payoff.Cmp(best) < 0 {
			best.Set(payoff)
		}
	}
	return best
}

// colCap returns max_i sum_j A[i][j] * y_j, the largest expected payoff
// the row player can obtain against the column mixed strategy y.
func (g *Game) colCap(y []*big.Rat) *big.Rat {
	best := new(big.Rat)
	for i := 0; i < g.M; i++ {
		payoff := new(big.Rat)
		for j := 0; j < g.N; j++ {
			payoff.Add(payoff, new(big.Rat).Mul(g.A[i][j], y[j]))
		}
		if i == 0 || payoff.Cmp(best) > 0 {
			best.Set(payoff)
		}
	}
	return best
}

// ValueBounds returns the pure-strategy bounds of the game:
// the row player's security level max_i min_j A[i][j] and the column
// player's security level min_j max_i A[i][j]. They are equal iff the
// game has a pure-strategy saddle point.
func (g *Game) ValueBounds() (lower, upper *big.Rat) {
	lower = new(big.Rat)
	for i := 0; i < g.M; i++ {
		rowMin := new(big.Rat).Set(g.A[i][0])
		for j := 1; j < g.N; j++ {
			if g.A[i][j].Cmp(rowMin) < 0 {
				rowMin.Set(g.A[i][j])
			}
		}
		if i == 0 || rowMin.Cmp(lower) > 0 {
			lower.Set(rowMin)
		}
	}
	upper = new(big.Rat)
	for j := 0; j < g.N; j++ {
		colMax := new(big.Rat).Set(g.A[0][j])
		for i := 1; i < g.M; i++ {
			if g.A[i][j].Cmp(colMax) > 0 {
				colMax.Set(g.A[i][j])
			}
		}
		if j == 0 || colMax.Cmp(upper) < 0 {
			upper.Set(colMax)
		}
	}
	return lower, upper
}

// SaddlePoint returns a pure-strategy saddle point (i, j, value) if the
// game has one: A[i][j] is the minimum of its row and the maximum of its
// column. When several exist, the first in row-major order is returned.
func (g *Game) SaddlePoint() (row, col int, value *big.Rat, ok bool) {
	for i := 0; i < g.M; i++ {
		for j := 0; j < g.N; j++ {
			v := g.A[i][j]
			rowMin, colMax := true, true
			for jj := 0; jj < g.N && rowMin; jj++ {
				if g.A[i][jj].Cmp(v) < 0 {
					rowMin = false
				}
			}
			for ii := 0; ii < g.M && colMax; ii++ {
				if g.A[ii][j].Cmp(v) > 0 {
					colMax = false
				}
			}
			if rowMin && colMax {
				return i, j, new(big.Rat).Set(v), true
			}
		}
	}
	return 0, 0, nil, false
}

func copyVec(p []*big.Rat) []*big.Rat {
	q := make([]*big.Rat, len(p))
	for i, v := range p {
		q[i] = new(big.Rat).Set(v)
	}
	return q
}

func ratString(r *big.Rat) string { return r.RatString() }

// String renders the certificate in a human-verifiable form.
func (c Certificate) String() string {
	return fmt.Sprintf("value=%s  row guarantees >= %s  column allows <= %s",
		ratString(c.Value), ratString(c.Lower), ratString(c.Upper))
}
