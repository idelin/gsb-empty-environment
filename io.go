package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// flexNumber accepts a JSON number, a quoted integer, or a quoted fraction
// such as "3/2", keeping the exact rational value.
type flexNumber struct {
	rat *big.Rat
}

func (f *flexNumber) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) > 0 && s[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		s = str
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid number %q", s)
	}
	f.rat = r
	return nil
}

type rawInput struct {
	Matrix       [][]flexNumber `json:"matrix"`
	Observations []flexNumber   `json:"observations"`
	Lower        []flexNumber   `json:"lower"`
	Upper        []flexNumber   `json:"upper"`
}

const maxDimension = 12

// parseProblem decodes the JSON instance and converts it to an exact
// integer problem: every observation row is multiplied by a nonzero
// constant to clear denominators (which does not change its solution set),
// and rational bounds are tightened to the nearest valid integers.
func parseProblem(data []byte) (*Problem, error) {
	var in rawInput
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("invalid JSON: %v", err)
	}
	n := len(in.Lower)
	if n == 0 {
		return nil, fmt.Errorf(`"lower" bounds are missing or empty`)
	}
	if len(in.Upper) != n {
		return nil, fmt.Errorf("len(upper) = %d, want %d", len(in.Upper), n)
	}
	if n > maxDimension {
		return nil, fmt.Errorf("dimension %d exceeds the maximum of %d", n, maxDimension)
	}
	m := len(in.Observations)
	if len(in.Matrix) != m {
		return nil, fmt.Errorf("matrix has %d rows but there are %d observations", len(in.Matrix), m)
	}

	A := make([][]*big.Int, m)
	b := make([]*big.Int, m)
	for i := 0; i < m; i++ {
		if len(in.Matrix[i]) != n {
			return nil, fmt.Errorf("matrix row %d has %d entries, want %d", i, len(in.Matrix[i]), n)
		}
		d := big.NewInt(1)
		for j := 0; j < n; j++ {
			if in.Matrix[i][j].rat == nil {
				return nil, fmt.Errorf("matrix entry (%d,%d) is missing", i, j)
			}
			d.Mul(d, in.Matrix[i][j].rat.Denom())
		}
		if in.Observations[i].rat == nil {
			return nil, fmt.Errorf("observation %d is missing", i)
		}
		d.Mul(d, in.Observations[i].rat.Denom())
		row := make([]*big.Int, n)
		for j := 0; j < n; j++ {
			row[j] = scaleRatToInt(in.Matrix[i][j].rat, d)
		}
		A[i] = row
		b[i] = scaleRatToInt(in.Observations[i].rat, d)
	}

	lo := make([]*big.Int, n)
	hi := make([]*big.Int, n)
	for i := 0; i < n; i++ {
		if in.Lower[i].rat == nil || in.Upper[i].rat == nil {
			return nil, fmt.Errorf("bound %d is missing", i)
		}
		lo[i] = ceilRat(in.Lower[i].rat)
		hi[i] = floorRat(in.Upper[i].rat)
	}
	return &Problem{m: m, n: n, A: A, b: b, lo: lo, hi: hi}, nil
}

// scaleRatToInt returns r*d, which must be an integer because d is a
// multiple of r's denominator.
func scaleRatToInt(r *big.Rat, d *big.Int) *big.Int {
	num := new(big.Int).Mul(r.Num(), d)
	return num.Quo(num, r.Denom())
}

// intList marshals big integers as raw JSON numbers (not strings).
type intList []*big.Int

func (l intList) MarshalJSON() ([]byte, error) {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, v := range l {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(v.String())
	}
	sb.WriteByte(']')
	return []byte(sb.String()), nil
}

type conflictJSON struct {
	Observations []int   `json:"observations"`
	Coefficients intList `json:"coefficients,omitempty"`
	Explanation  string  `json:"explanation"`
}

type outputJSON struct {
	Status   string        `json:"status"`
	Signal   intList       `json:"signal,omitempty"`
	Signals  []intList     `json:"signals,omitempty"`
	Conflict *conflictJSON `json:"conflict,omitempty"`
}

func toJSON(res *Result) outputJSON {
	out := outputJSON{Status: res.Status}
	switch res.Status {
	case "unique":
		out.Signal = intList(res.Signals[0])
	case "multiple":
		out.Signals = []intList{intList(res.Signals[0]), intList(res.Signals[1])}
	case "infeasible":
		out.Conflict = &conflictJSON{
			Observations: res.Conflict.Observations,
			Explanation:  res.Conflict.Explanation,
		}
		if res.Conflict.Coefficients != nil {
			out.Conflict.Coefficients = intList(res.Conflict.Coefficients)
		}
	}
	return out
}
