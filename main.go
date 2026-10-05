// Command pairrank computes the exact top-K ranking timeline of 2D points
// moving with constant rational velocities, relative to a static query point,
// over a rational time interval [0,T]. Event times are exact algebraic
// numbers expressed as u + v*sqrt(m) together with a rational isolating
// interval and a witnessing polynomial. Only the Go standard library is used.
//
// Usage:
//
//	pairrank [input.json]     (reads stdin when no file is given)
//
// Input JSON:
//
//	{
//	  "query": {"x": "0", "y": "0"},
//	  "T": "4",
//	  "K": 2,
//	  "objects": [
//	    {"id": 1, "x": "5", "y": "0", "vx": "-2", "vy": "0"}
//	  ]
//	}
//
// All numbers are rationals given as strings ("3/2", "1.5", "-2") or JSON
// numbers. K defaults to the number of objects; query defaults to (0,0).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

type rawNum struct {
	r *big.Rat
}

func (rn *rawNum) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	s = strings.Trim(s, `"`)
	if s == "" {
		rn.r = new(big.Rat)
		return nil
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational number %q", s)
	}
	rn.r = r
	return nil
}

func (rn rawNum) rat() *big.Rat {
	if rn.r == nil {
		return new(big.Rat)
	}
	return rn.r
}

type inputJSON struct {
	Query *struct {
		X rawNum `json:"x"`
		Y rawNum `json:"y"`
	} `json:"query"`
	T       rawNum `json:"T"`
	K       *int   `json:"K"`
	Objects []struct {
		ID int64  `json:"id"`
		X  rawNum `json:"x"`
		Y  rawNum `json:"y"`
		Vx rawNum `json:"vx"`
		Vy rawNum `json:"vy"`
	} `json:"objects"`
}

type algJSON struct {
	Expr   string  `json:"expr"`
	U      string  `json:"u"`
	V      string  `json:"v"`
	Sqrt   string  `json:"sqrt"`
	Approx float64 `json:"approx"`
}

func toAlgJSON(a *AlgNum) algJSON {
	return algJSON{
		Expr:   a.String(),
		U:      a.u.RatString(),
		V:      a.v.RatString(),
		Sqrt:   a.m.String(),
		Approx: a.approx(),
	}
}

type entryJSON struct {
	Type     string     `json:"type"` // "point" or "segment"
	Time     *algJSON   `json:"time,omitempty"`
	Start    *algJSON   `json:"start,omitempty"`
	End      *algJSON   `json:"end,omitempty"`
	Sample   string     `json:"sample,omitempty"`
	Boundary bool       `json:"boundary,omitempty"`
	Pairs    [][2]int64 `json:"tied_pairs,omitempty"`
	Poly     string     `json:"difference_polynomial,omitempty"`
	Interval []string   `json:"isolating_interval,omitempty"`
	Ranking  []int64    `json:"ranking"`
	TopK     []int64    `json:"topK"`
}

type outputJSON struct {
	Query        [2]string   `json:"query"`
	T            string      `json:"T"`
	K            int         `json:"K"`
	NumObjects   int         `json:"numObjects"`
	Timeline     []entryJSON `json:"timeline"`
	Verification struct {
		OK       bool     `json:"ok"`
		Checks   []string `json:"checks"`
		Failures []string `json:"failures,omitempty"`
	} `json:"verification"`
}

func run(input []byte) ([]byte, bool, error) {
	var in inputJSON
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, false, fmt.Errorf("parsing input: %w", err)
	}
	objs := make([]Object, len(in.Objects))
	seen := map[int64]bool{}
	for i, o := range in.Objects {
		if seen[o.ID] {
			return nil, false, fmt.Errorf("duplicate object id %d", o.ID)
		}
		seen[o.ID] = true
		objs[i] = Object{ID: o.ID, X: o.X.rat(), Y: o.Y.rat(), Vx: o.Vx.rat(), Vy: o.Vy.rat()}
	}
	qx, qy := new(big.Rat), new(big.Rat)
	if in.Query != nil {
		qx, qy = in.Query.X.rat(), in.Query.Y.rat()
	}
	T := in.T.rat()
	if T.Sign() < 0 {
		return nil, false, fmt.Errorf("T must be >= 0")
	}
	K := len(objs)
	if in.K != nil {
		K = *in.K
	}

	c := Compute(objs, qx, qy, T, K)
	checks, failures := c.Verify()

	var out outputJSON
	out.Query = [2]string{qx.RatString(), qy.RatString()}
	out.T = T.RatString()
	out.K = K
	out.NumObjects = len(objs)
	for _, e := range c.Timeline {
		if e.Event != nil {
			ev := e.Event
			aj := toAlgJSON(ev.T)
			ent := entryJSON{
				Type:     "point",
				Time:     &aj,
				Boundary: ev.Boundary,
				Interval: []string{ev.Lo.RatString(), ev.Hi.RatString()},
				Ranking:  ev.Ranking,
				TopK:     topK(ev.Ranking, K),
			}
			for _, pr := range ev.Pairs {
				ent.Pairs = append(ent.Pairs, [2]int64{objs[pr[0]].ID, objs[pr[1]].ID})
			}
			if len(ev.Pairs) > 0 {
				d := c.Dist[ev.Pairs[0][0]].Sub(c.Dist[ev.Pairs[0][1]])
				ent.Poly = d.String()
			}
			out.Timeline = append(out.Timeline, ent)
		} else {
			s := e.Segment
			ajA, ajB := toAlgJSON(s.A), toAlgJSON(s.B)
			out.Timeline = append(out.Timeline, entryJSON{
				Type:    "segment",
				Start:   &ajA,
				End:     &ajB,
				Sample:  s.Sample.RatString(),
				Ranking: s.Ranking,
				TopK:    topK(s.Ranking, K),
			})
		}
	}
	out.Verification.OK = len(failures) == 0
	out.Verification.Checks = checks
	out.Verification.Failures = failures

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, false, err
	}
	return data, len(failures) == 0, nil
}

func main() {
	var input []byte
	var err error
	if len(os.Args) > 1 {
		input, err = os.ReadFile(os.Args[1])
	} else {
		input, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	out, ok, err := run(input)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	fmt.Println(string(out))
	if !ok {
		fmt.Fprintln(os.Stderr, "VERIFICATION FAILED")
		os.Exit(1)
	}
}
