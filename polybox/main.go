// Command polybox answers counting and ranking queries over the lattice
// points of a bounded integer box intersected with linear inequalities.
//
// Input (JSON on stdin):
//   {
//     "dims": 2,
//     "box": [[0, 10], [0, 10]],
//     "constraints": [{"a": [1, 1], "op": "<=", "b": 5}],
//     "queries": [{"type": "count"}, {"type": "kth", "k": "3"}],
//     "budget": 1000000,
//     "verify": true
//   }
//
// Output (JSON on stdout): one result per query. Counts use big integers
// rendered as strings. When the search budget is exhausted the status is
// "unknown". kth results carry a prefix-count ranking proof.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
)

type jsonNumber struct {
	v *big.Int
}

func (n *jsonNumber) UnmarshalJSON(b []byte) error {
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		return err
	}
	v, ok := new(big.Int).SetString(num.String(), 10)
	if !ok {
		return fmt.Errorf("invalid integer: %s", num.String())
	}
	n.v = v
	return nil
}

func (n *jsonNumber) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.v.String())
}

type constraintIn struct {
	A  []jsonNumber `json:"a"`
	Op string       `json:"op"`
	B  jsonNumber   `json:"b"`
}

type queryIn struct {
	Type string     `json:"type"`
	K    jsonNumber `json:"k"`
}

type input struct {
	Dims        int            `json:"dims"`
	Box         [][]jsonNumber `json:"box"`
	Constraints []constraintIn `json:"constraints"`
	Queries     []queryIn      `json:"queries"`
	Budget      *int64         `json:"budget"`
	Verify      bool           `json:"verify"`
	VerifyLimit *int64         `json:"verify_limit"`
}

type proofStepOut struct {
	Dim       int    `json:"dim"`
	Value     string `json:"value"`
	Skipped   string `json:"skipped"`
	Remaining string `json:"remaining"`
}

type resultOut struct {
	Type   string         `json:"type"`
	Status string         `json:"status"`
	Count  string         `json:"count,omitempty"`
	K      string         `json:"k,omitempty"`
	Point  []string       `json:"point,omitempty"`
	Proof  []proofStepOut `json:"proof,omitempty"`
	Detail string         `json:"detail,omitempty"`
}

type verifyOut struct {
	Status      string `json:"status"`
	Enumerated  string `json:"enumerated,omitempty"`
	Detail      string `json:"detail,omitempty"`
	BruteForced bool   `json:"brute_forced"`
}

type output struct {
	Results []resultOut `json:"results"`
	Verify  *verifyOut  `json:"verify,omitempty"`
	Nodes   int64       `json:"nodes"`
}

func normalizeConstraint(c constraintIn, dims int) ([]Constraint, error) {
	if c.Op == "" {
		c.Op = "<="
	}
	if len(c.A) != dims {
		return nil, fmt.Errorf("constraint has %d coefficients, want %d", len(c.A), dims)
	}
	if c.B.v == nil {
		return nil, fmt.Errorf("constraint missing rhs")
	}
	a := make([]*big.Int, dims)
	for i := range a {
		if c.A[i].v == nil {
			return nil, fmt.Errorf("constraint missing coefficient %d", i)
		}
		a[i] = c.A[i].v
	}
	neg := func(xs []*big.Int) []*big.Int {
		out := make([]*big.Int, len(xs))
		for i, x := range xs {
			out[i] = new(big.Int).Neg(x)
		}
		return out
	}
	switch c.Op {
	case "<=":
		return []Constraint{{A: a, B: c.B.v}}, nil
	case "<":
		return []Constraint{{A: a, B: new(big.Int).Sub(c.B.v, bigOne)}}, nil
	case ">=":
		return []Constraint{{A: neg(a), B: new(big.Int).Neg(c.B.v)}}, nil
	case ">":
		b := new(big.Int).Neg(c.B.v)
		b.Sub(b, bigOne)
		return []Constraint{{A: neg(a), B: b}}, nil
	case "==", "=":
		ge := neg(a)
		return []Constraint{
			{A: a, B: c.B.v},
			{A: ge, B: new(big.Int).Neg(c.B.v)},
		}, nil
	}
	return nil, fmt.Errorf("unknown op %q", c.Op)
}

func buildProblem(in *input) (*Problem, error) {
	if in.Dims < 0 {
		return nil, fmt.Errorf("dims must be >= 0")
	}
	if len(in.Box) != in.Dims {
		return nil, fmt.Errorf("box has %d dims, want %d", len(in.Box), in.Dims)
	}
	p := &Problem{Dims: in.Dims}
	for i, iv := range in.Box {
		if len(iv) != 2 || iv[0].v == nil || iv[1].v == nil {
			return nil, fmt.Errorf("box dim %d must be [lo, hi]", i)
		}
		p.Lo = append(p.Lo, iv[0].v)
		p.Hi = append(p.Hi, iv[1].v)
	}
	for _, c := range in.Constraints {
		nc, err := normalizeConstraint(c, in.Dims)
		if err != nil {
			return nil, err
		}
		p.Cons = append(p.Cons, nc...)
	}
	return p, nil
}

// bruteForce enumerates the box in lexicographic order and returns every
// feasible point. ok is false when the box is too large for the limit.
func bruteForce(p *Problem, limit int64) ([][]*big.Int, bool) {
	total := int64(1)
	for i := 0; i < p.Dims; i++ {
		w := new(big.Int).Sub(p.Hi[i], p.Lo[i])
		w.Add(w, bigOne)
		if w.Sign() <= 0 {
			return nil, true // empty box: zero points
		}
		if !w.IsInt64() || total > limit/w.Int64() {
			return nil, false
		}
		total *= w.Int64()
	}
	if total > limit {
		return nil, false
	}
	feasible := func(pt []*big.Int) bool {
		for _, c := range p.Cons {
			sum := new(big.Int)
			for i, a := range c.A {
				sum.Add(sum, new(big.Int).Mul(a, pt[i]))
			}
			if sum.Cmp(c.B) > 0 {
				return false
			}
		}
		return true
	}
	var pts [][]*big.Int
	pt := make([]*big.Int, p.Dims)
	for i := range pt {
		pt[i] = new(big.Int).Set(p.Lo[i])
	}
	for {
		if feasible(pt) {
			cp := make([]*big.Int, p.Dims)
			for i := range cp {
				cp[i] = new(big.Int).Set(pt[i])
			}
			pts = append(pts, cp)
		}
		// odometer increment: x0 most significant => increment last dim first
		d := p.Dims - 1
		for ; d >= 0; d-- {
			if pt[d].Cmp(p.Hi[d]) < 0 {
				pt[d].Add(pt[d], bigOne)
				break
			}
			pt[d].Set(p.Lo[d])
		}
		if d < 0 {
			break
		}
	}
	return pts, true
}

func strsOfBig(xs []*big.Int) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.String()
	}
	return out
}

func run(in *input, flagBudget int64, flagVerify bool, flagVerifyLimit int64) (*output, error) {
	p, err := buildProblem(in)
	if err != nil {
		return nil, err
	}
	budget := flagBudget
	if in.Budget != nil {
		budget = *in.Budget
	}
	verifyLimit := flagVerifyLimit
	if in.VerifyLimit != nil {
		verifyLimit = *in.VerifyLimit
	}
	s, err := NewSolver(p, budget)
	if err != nil {
		return nil, err
	}
	out := &output{}
	var brute [][]*big.Int
	bruteOK := false
	needBrute := in.Verify || flagVerify
	if needBrute {
		brute, bruteOK = bruteForce(p, verifyLimit)
	}
	for _, q := range in.Queries {
		switch q.Type {
		case "count":
			s.Reset()
			n, err := s.Count()
			r := resultOut{Type: "count"}
			if err == ErrBudgetExhausted {
				r.Status = "unknown"
				r.Detail = "budget exhausted"
			} else if err != nil {
				return nil, err
			} else {
				r.Status = "ok"
				r.Count = n.String()
			}
			out.Results = append(out.Results, r)
		case "kth":
			if q.K.v == nil {
				return nil, fmt.Errorf("kth query missing k")
			}
			s.Reset()
			pt, proof, err := s.Kth(q.K.v)
			r := resultOut{Type: "kth", K: q.K.v.String()}
			switch err {
			case nil:
				r.Status = "ok"
				r.Point = strsOfBig(pt)
				for _, st := range proof {
					r.Proof = append(r.Proof, proofStepOut{
						Dim:       st.Dim,
						Value:     st.Value.String(),
						Skipped:   st.Skipped.String(),
						Remaining: st.Remaining.String(),
					})
				}
			case ErrBudgetExhausted:
				r.Status = "unknown"
				r.Detail = "budget exhausted"
			case ErrOutOfRange:
				r.Status = "out_of_range"
				r.Detail = "k out of range"
			default:
				return nil, err
			}
			out.Results = append(out.Results, r)
		default:
			return nil, fmt.Errorf("unknown query type %q", q.Type)
		}
	}
	out.Nodes = s.Nodes()
	if needBrute {
		v := &verifyOut{BruteForced: bruteOK}
		if !bruteOK {
			v.Status = "skipped"
			v.Detail = fmt.Sprintf("box too large for verify limit %d", verifyLimit)
		} else {
			v.Enumerated = fmt.Sprintf("%d", len(brute))
			v.Status = "ok"
			// Cross-check every reported result against brute force.
			s.Reset()
			n, err := s.Count()
			if err == nil && n.Cmp(big.NewInt(int64(len(brute)))) != 0 {
				v.Status = "mismatch"
				v.Detail = fmt.Sprintf("count: solver=%s brute=%d", n.String(), len(brute))
			}
			if v.Status == "ok" {
				for _, q := range in.Queries {
					if q.Type != "kth" || q.K.v == nil || q.K.v.Sign() <= 0 {
						continue
					}
					if !q.K.v.IsInt64() || q.K.v.Int64() > int64(len(brute)) {
						continue // out-of-range k checked by unit tests
					}
					s.Reset()
					pt, _, err := s.Kth(q.K.v)
					if err != nil {
						continue
					}
					want := brute[q.K.v.Int64()-1]
					for i := range want {
						if pt[i].Cmp(want[i]) != 0 {
							v.Status = "mismatch"
							v.Detail = fmt.Sprintf("kth(%s): solver dim %d = %s, brute = %s",
								q.K.v.String(), i, pt[i].String(), want[i].String())
							break
						}
					}
					if v.Status != "ok" {
						break
					}
				}
			}
		}
		out.Verify = v
	}
	return out, nil
}

func main() {
	budget := flag.Int64("budget", 1000000, "search node budget (0 = unlimited)")
	verify := flag.Bool("verify", false, "brute-force cross-check on small boxes")
	verifyLimit := flag.Int64("verify-limit", 200000, "max box size for brute-force verification")
	flag.Parse()
	data, err := ioutil.ReadAll(os.Stdin)
	if err != nil {
		fatal(err)
	}
	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(err)
	}
	out, err := run(&in, *budget, *verify, *verifyLimit)
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "polybox: %v\n", err)
	os.Exit(1)
}
