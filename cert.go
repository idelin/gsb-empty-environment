package main

import (
	"fmt"
	"math/big"
)

// Certificate JSON schema. All rationals are strings ("3/4" or "5").

type IvJSON struct {
	L string `json:"l"`
	R string `json:"r"`
}

type StatJSON struct {
	L            string `json:"l"`
	R            string `json:"r"`
	Multiplicity int    `json:"multiplicity"`
	Left         string `json:"left"`
	Right        string `json:"right"`
	Role         string `json:"role"`
}

type ExtremumJSON struct {
	ValueL string   `json:"value_l"`
	ValueR string   `json:"value_r"`
	Params []IvJSON `json:"params"`
}

type Cert struct {
	Values   []string     `json:"values"`
	Weights  []string     `json:"weights"`
	Epsilon  string       `json:"epsilon"`
	DerivNum []string     `json:"derivative_numerator"` // integer coeffs, low -> high
	Constant bool         `json:"constant"`
	Stats    []StatJSON   `json:"stationary_points"`
	Max      ExtremumJSON `json:"global_max"`
	Min      ExtremumJSON `json:"global_min"`
}

func ivJSON(iv Interval) IvJSON {
	return IvJSON{L: iv.L.RatString(), R: iv.R.RatString()}
}

func paramsJSON(ps []Interval) []IvJSON {
	out := make([]IvJSON, len(ps))
	for i, p := range ps {
		out[i] = ivJSON(p)
	}
	return out
}

// certFromSolution renders a solution as a certificate.
func certFromSolution(sol *Solution) *Cert {
	c := sol.Curve
	cert := &Cert{
		Constant: sol.Constant,
		Epsilon:  sol.Eps.RatString(),
	}
	for _, v := range c.Values {
		cert.Values = append(cert.Values, v.RatString())
	}
	for _, w := range c.Weights {
		cert.Weights = append(cert.Weights, w.RatString())
	}
	cert.DerivNum = polyStrings(clearDenoms(sol.P))
	for _, st := range sol.Stats {
		cert.Stats = append(cert.Stats, StatJSON{
			L:            st.Iv.L.RatString(),
			R:            st.Iv.R.RatString(),
			Multiplicity: st.Mult,
			Left:         st.Left,
			Right:        st.Right,
			Role:         st.Role,
		})
	}
	cert.Max = ExtremumJSON{
		ValueL: sol.MaxVal.L.RatString(),
		ValueR: sol.MaxVal.R.RatString(),
		Params: paramsJSON(sol.MaxParams),
	}
	cert.Min = ExtremumJSON{
		ValueL: sol.MinVal.L.RatString(),
		ValueR: sol.MinVal.R.RatString(),
		Params: paramsJSON(sol.MinParams),
	}
	return cert
}

func (c *Cert) curve() (Curve, error) {
	if len(c.Values) != len(c.Weights) {
		return Curve{}, fmt.Errorf("values/weights length mismatch")
	}
	cv := Curve{}
	for _, s := range c.Values {
		v, ok := parseRat(s)
		if !ok {
			return Curve{}, fmt.Errorf("bad control value %q", s)
		}
		cv.Values = append(cv.Values, v)
	}
	for _, s := range c.Weights {
		w, ok := parseRat(s)
		if !ok {
			return Curve{}, fmt.Errorf("bad weight %q", s)
		}
		cv.Weights = append(cv.Weights, w)
	}
	return cv, nil
}

func (c *Cert) epsilon() (*big.Rat, error) {
	e, ok := parseRat(c.Epsilon)
	if !ok || e.Sign() <= 0 {
		return nil, fmt.Errorf("bad epsilon %q", c.Epsilon)
	}
	return e, nil
}
