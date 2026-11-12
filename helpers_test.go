package netsec

import (
	"math/big"
)

// dealerMaterial is a simulated dealer output: polynomial coefficients,
// the Feldman commitment and per-receiver shares s_i = f(x_i).
type dealerMaterial struct {
	id     string
	coeffs []*big.Int
	commit Commitment
	shares map[string]*big.Int
}

func randScalar() *big.Int {
	n := Curve.Params().N
	for {
		s, err := randInt(n)
		if err != nil {
			panic(err)
		}
		if s.Sign() > 0 {
			return s
		}
	}
}

// genDealer builds a degree t-1 polynomial and its Feldman commitment.
func genDealer(id string, t int) *dealerMaterial {
	m := &dealerMaterial{id: id, shares: map[string]*big.Int{}}
	for i := 0; i < t; i++ {
		m.coeffs = append(m.coeffs, randScalar())
	}
	for _, a := range m.coeffs {
		x, y := Curve.ScalarBaseMult(a.Bytes())
		m.commit.Points = append(m.commit.Points, Point{X: x, Y: y})
	}
	return m
}

// shareAt evaluates the dealer's polynomial at x.
func (m *dealerMaterial) shareAt(x *big.Int) *big.Int {
	n := Curve.Params().N
	value := new(big.Int)
	power := big.NewInt(1)
	for _, a := range m.coeffs {
		term := new(big.Int).Mul(a, power)
		value.Add(value, term)
		power = new(big.Int).Mul(power, x)
		power.Mod(power, n)
	}
	return value.Mod(value, n)
}

func (m *dealerMaterial) issue(receiverID string, x *big.Int) *big.Int {
	s := m.shareAt(x)
	m.shares[receiverID] = s
	return s
}

// cloneCommit returns a deep copy so tests can mutate one broadcast.
func cloneCommit(c Commitment) Commitment {
	out := Commitment{Points: make([]Point, len(c.Points))}
	for i, p := range c.Points {
		out.Points[i] = Point{X: new(big.Int).Set(p.X), Y: new(big.Int).Set(p.Y)}
	}
	return out
}

func infinityPoint() Point {
	return Point{X: new(big.Int), Y: new(big.Int)}
}

func generatorPoint() Point {
	x, y := Curve.ScalarBaseMult(big.NewInt(1).Bytes())
	return Point{X: x, Y: y}
}

func mustRegisterRun(t testTB, e *Engine, dealerIDs, receiverIDs []string, tThreshold int) (map[string]*dealerMaterial, map[string]*big.Int) {
	t.Helper()
	materials := map[string]*dealerMaterial{}
	xs := map[string]*big.Int{}
	for i, rid := range receiverIDs {
		x := big.NewInt(int64(i + 1))
		xs[rid] = x
		if err := e.RegisterReceiver(rid, x); err != nil {
			t.Fatalf("register receiver %s: %v", rid, err)
		}
	}
	for _, did := range dealerIDs {
		m := genDealer(did, tThreshold)
		materials[did] = m
		if err := e.RegisterCommitment(did, m.commit); err != nil {
			t.Fatalf("register commitment %s: %v", did, err)
		}
		hash := m.commit.Hash()
		for _, rid := range receiverIDs {
			if err := e.RegisterReceipt(did, rid, hash); err != nil {
				t.Fatalf("register receipt %s/%s: %v", did, rid, err)
			}
			s := m.issue(rid, xs[rid])
			if err := e.RegisterShare(did, rid, s); err != nil {
				t.Fatalf("register share %s/%s: %v", did, rid, err)
			}
		}
	}
	return materials, xs
}

type testTB interface {
	Helper()
	Fatalf(format string, args ...any)
}
