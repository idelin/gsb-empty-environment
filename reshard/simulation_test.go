package reshard

import (
	"errors"
	"math/big"
	"testing"
)

// simulate independently applies ops to the source layout and recomputes
// every primitive cost directly as an exact rational, serving as an oracle
// for both semantic reachability and per-step cost formulas.
func simulate(p Problem, ops []Op) (Layout, *big.Rat, error) {
	cur := newState(p.Source)
	total := new(big.Rat)
	for _, op := range ops {
		perCard := perCardRat(p, cur)
		full := new(big.Rat).SetFrac(fullBytes(p), big.NewInt(1))
		switch op.Type {
		case AllGather:
			if cur.partial[op.A] || axisShard(cur, op.A) != op.D1 {
				return Layout{}, nil, errors.New("invalid AllGather precondition")
			}
			cur.shard[op.D1] = -1
			total.Add(total, volRat(p, op.A, full, 1))
		case AllReduce:
			if !cur.partial[op.A] {
				return Layout{}, nil, errors.New("invalid AllReduce precondition")
			}
			cur.partial[op.A] = false
			total.Add(total, volRat(p, op.A, perCard, 2))
		case AllToAll:
			if cur.partial[op.A] || axisShard(cur, op.A) != op.D1 ||
				cur.shard[op.D2] != -1 || cur.partial[op.B] ||
				(op.B != op.A && axisShard(cur, op.B) >= 0) {
				return Layout{}, nil, errors.New("invalid AllToAll precondition")
			}
			if p.Shape[op.D2]%p.Mesh.Ranks[op.B] != 0 {
				return Layout{}, nil, errors.New("non-divisible AllToAll split")
			}
			cur.shard[op.D1] = -1
			cur.shard[op.D2] = op.B
			total.Add(total, volRat(p, op.A, perCard, 1))
		case LocalSlice:
			if cur.partial[op.A] || axisShard(cur, op.A) >= 0 ||
				cur.shard[op.D2] != -1 {
				return Layout{}, nil, errors.New("invalid LocalSlice precondition")
			}
			if p.Shape[op.D2]%p.Mesh.Ranks[op.A] != 0 {
				return Layout{}, nil, errors.New("non-divisible LocalSlice split")
			}
			cur.shard[op.D2] = op.A
		case ReduceScatter:
			if !cur.partial[op.A] || cur.shard[op.D2] != -1 {
				return Layout{}, nil, errors.New("invalid ReduceScatter precondition")
			}
			if p.Shape[op.D2]%p.Mesh.Ranks[op.A] != 0 {
				return Layout{}, nil, errors.New("non-divisible ReduceScatter split")
			}
			cur.partial[op.A] = false
			cur.shard[op.D2] = op.A
			total.Add(total, volRat(p, op.A, perCard, 1))
		default:
			return Layout{}, nil, errors.New("unknown primitive")
		}
	}
	return cur.layout(), total, nil
}

func perCardRat(p Problem, s state) *big.Rat {
	num := new(big.Int).SetInt64(p.BytesPerElement)
	den := int64(1)
	for d, n := range p.Shape {
		num.Mul(num, big.NewInt(n))
		if a := s.shard[d]; a >= 0 {
			den *= p.Mesh.Ranks[a]
		}
	}
	return new(big.Rat).SetFrac(num, big.NewInt(den))
}

// volRat computes factor * bytes * (M_a-1)/M_a * w_a as an exact rational.
func volRat(p Problem, a int, bytes *big.Rat, factor int64) *big.Rat {
	m := p.Mesh.Ranks[a]
	r := new(big.Rat).Set(bytes)
	r.Mul(r, big.NewRat((m-1)*p.Mesh.Weights[a]*factor, m))
	return r
}

func simulatedCost(t *testing.T, p Problem, ops []Op) *big.Rat {
	t.Helper()
	end, cost, err := simulate(p, ops)
	if err != nil {
		t.Fatalf("simulation rejected solver plan: %v", err)
	}
	if !newState(end).equal(newState(p.Target)) {
		t.Fatalf("solver plan ends at %v, want target %v", end, p.Target)
	}
	return cost
}
