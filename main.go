package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
)

const defaultBudget int64 = 1_000_000

type constraintJSON struct {
	Coeffs []json.Number `json:"coeffs"`
	Rhs    json.Number   `json:"rhs"`
}

type queryJSON struct {
	Type  string      `json:"type"`
	K     json.Number `json:"k"`
	Limit int         `json:"limit"`
}

type docJSON struct {
	Dim         int              `json:"dim"`
	Box         [][2]json.Number `json:"box"`
	Constraints []constraintJSON `json:"constraints"`
	Budget      int64            `json:"budget"`
	Queries     []queryJSON      `json:"queries"`
}

type proofStepJSON struct {
	Dim     int    `json:"dim"`
	Value   string `json:"value"`
	Skipped string `json:"skipped"`
}

type resultJSON struct {
	Type      string          `json:"type"`
	Status    string          `json:"status"` // ok | unknown | out_of_range | too_many | error
	Count     string          `json:"count,omitempty"`
	Point     []string        `json:"point,omitempty"`
	Points    [][]string      `json:"points,omitempty"`
	Proof     []proofStepJSON `json:"proof,omitempty"`
	Total     string          `json:"total,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
	Error     string          `json:"error,omitempty"`
}

func parseBig(n json.Number) (*big.Int, error) {
	v, ok := new(big.Int).SetString(n.String(), 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", n.String())
	}
	return v, nil
}

func bigsToStrings(vs []*big.Int) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}

func run() error {
	dec := json.NewDecoder(os.Stdin)
	dec.UseNumber()
	var d docJSON
	if err := dec.Decode(&d); err != nil {
		return fmt.Errorf("invalid input JSON: %w", err)
	}
	if d.Dim < 0 || len(d.Box) != d.Dim {
		return fmt.Errorf("dim (%d) must equal len(box) (%d)", d.Dim, len(d.Box))
	}
	lo := make([]*big.Int, d.Dim)
	hi := make([]*big.Int, d.Dim)
	for i, b := range d.Box {
		var err error
		if lo[i], err = parseBig(b[0]); err != nil {
			return fmt.Errorf("box[%d] lower: %w", i, err)
		}
		if hi[i], err = parseBig(b[1]); err != nil {
			return fmt.Errorf("box[%d] upper: %w", i, err)
		}
	}
	cons := make([]constraint, len(d.Constraints))
	for i, c := range d.Constraints {
		if len(c.Coeffs) != d.Dim {
			return fmt.Errorf("constraint %d: need %d coefficients, got %d", i, d.Dim, len(c.Coeffs))
		}
		coeffs := make([]*big.Int, d.Dim)
		for j, n := range c.Coeffs {
			v, err := parseBig(n)
			if err != nil {
				return fmt.Errorf("constraint %d coeff %d: %w", i, j, err)
			}
			coeffs[j] = v
		}
		rhs, err := parseBig(c.Rhs)
		if err != nil {
			return fmt.Errorf("constraint %d rhs: %w", i, err)
		}
		cons[i] = constraint{coeffs: coeffs, rhs: rhs}
	}
	budget := d.Budget
	if budget == 0 {
		budget = defaultBudget
	} else if budget < 0 {
		budget = int64(^uint64(0) >> 1) // unlimited
	}

	results := make([]resultJSON, 0, len(d.Queries))
	for _, q := range d.Queries {
		switch q.Type {
		case "count":
			s := NewSolver(lo, hi, cons, budget)
			total, ok := s.Count()
			if !ok {
				results = append(results, resultJSON{Type: q.Type, Status: "unknown"})
			} else {
				results = append(results, resultJSON{Type: q.Type, Status: "ok", Count: total.String()})
			}
		case "kth":
			if q.K == "" {
				results = append(results, resultJSON{Type: q.Type, Status: "error", Error: "missing k"})
				continue
			}
			k, err := parseBig(q.K)
			if err != nil {
				results = append(results, resultJSON{Type: q.Type, Status: "error", Error: err.Error()})
				continue
			}
			counter := NewSolver(lo, hi, cons, budget)
			total, ok := counter.Count()
			if !ok {
				results = append(results, resultJSON{Type: q.Type, Status: "unknown"})
				continue
			}
			if k.Sign() < 1 || k.Cmp(total) > 0 {
				results = append(results, resultJSON{Type: q.Type, Status: "out_of_range", Total: total.String()})
				continue
			}
			s := NewSolver(lo, hi, cons, budget)
			pt, proof, ok := s.Kth(k)
			if !ok {
				results = append(results, resultJSON{Type: q.Type, Status: "unknown"})
				continue
			}
			pj := make([]proofStepJSON, len(proof))
			for i, st := range proof {
				pj[i] = proofStepJSON{Dim: st.Dim, Value: st.Value.String(), Skipped: st.Skipped.String()}
			}
			results = append(results, resultJSON{
				Type:   q.Type,
				Status: "ok",
				Point:  bigsToStrings(pt),
				Proof:  pj,
				Total:  total.String(),
			})
		case "enumerate":
			limit := q.Limit
			if limit <= 0 {
				limit = 10000
			}
			pts, truncated := Enumerate(lo, hi, cons, limit)
			status := "ok"
			if truncated {
				status = "too_many"
			}
			out := make([][]string, len(pts))
			for i, p := range pts {
				out[i] = bigsToStrings(p)
			}
			results = append(results, resultJSON{
				Type:      q.Type,
				Status:    status,
				Points:    out,
				Count:     fmt.Sprintf("%d", len(pts)),
				Truncated: truncated,
			})
		default:
			results = append(results, resultJSON{Type: q.Type, Status: "error", Error: "unknown query type"})
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"results": results})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, `{"error": %q}`+"\n", err.Error())
		os.Exit(1)
	}
}
