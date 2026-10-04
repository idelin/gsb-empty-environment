package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

// RatJSON decodes a rational given as a JSON string ("3/2", "-1.5", "4")
// or a JSON number.
type RatJSON struct{ V *big.Rat }

func (r *RatJSON) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	v, ok := new(big.Rat).SetString(s)
	if !ok {
		return fmt.Errorf("invalid rational %q", s)
	}
	r.V = v
	return nil
}

type jsonObject struct {
	ID string  `json:"id"`
	X  RatJSON `json:"x"`
	Y  RatJSON `json:"y"`
	VX RatJSON `json:"vx"`
	VY RatJSON `json:"vy"`
}

type jsonInput struct {
	Query struct {
		X RatJSON `json:"x"`
		Y RatJSON `json:"y"`
	} `json:"query"`
	T       RatJSON      `json:"T"`
	K       int          `json:"K"`
	Objects []jsonObject `json:"objects"`
}

func (ji *jsonInput) toInput() (Input, error) {
	in := Input{
		QueryX: ji.Query.X.V,
		QueryY: ji.Query.Y.V,
		T:      ji.T.V,
		K:      ji.K,
	}
	if in.QueryX == nil || in.QueryY == nil || in.T == nil {
		return in, fmt.Errorf("query.x, query.y and T are required")
	}
	if in.T.Sign() < 0 {
		return in, fmt.Errorf("T must be >= 0")
	}
	seen := map[string]bool{}
	for _, o := range ji.Objects {
		if o.ID == "" {
			return in, fmt.Errorf("object with empty id")
		}
		if seen[o.ID] {
			return in, fmt.Errorf("duplicate object id %q", o.ID)
		}
		seen[o.ID] = true
		for name, v := range map[string]*big.Rat{"x": o.X.V, "y": o.Y.V, "vx": o.VX.V, "vy": o.VY.V} {
			if v == nil {
				return in, fmt.Errorf("object %q: missing %s", o.ID, name)
			}
		}
		in.Objects = append(in.Objects, Object{
			ID: o.ID, X: o.X.V, Y: o.Y.V, VX: o.VX.V, VY: o.VY.V,
		})
	}
	return in, nil
}

func topIDs(e *Engine, ranking []int, k int) string {
	if k > len(ranking) {
		k = len(ranking)
	}
	if k < 0 {
		k = 0
	}
	ids := make([]string, k)
	for i := 0; i < k; i++ {
		ids[i] = e.ids[ranking[i]]
	}
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, " ")
}

func tiesString(e *Engine, ev *Event) string {
	var parts []string
	for p := range ev.Pairs {
		parts = append(parts, e.ids[p.i]+"="+e.ids[p.j])
	}
	for i := 0; i < e.n; i++ {
		for j := i + 1; j < e.n; j++ {
			if e.identical[i][j] {
				parts = append(parts, e.ids[i]+"="+e.ids[j])
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "   ties: " + strings.Join(parts, " ")
}

func printTimeline(w io.Writer, e *Engine, tl *Timeline) {
	fmt.Fprintf(w, "objects=%d  K=%d  T=%s\n", e.n, e.in.K, ratStr(e.in.T))
	for i := 0; i < e.n; i++ {
		for j := i + 1; j < e.n; j++ {
			if e.identical[i][j] {
				fmt.Fprintf(w, "note: %s and %s have identical distance functions (permanent tie, broken by ID)\n",
					e.ids[i], e.ids[j])
			}
		}
	}
	for idx, ev := range tl.Events {
		fmt.Fprintf(w, "event   t=%-40s top-%d: %s%s\n",
			ev.Time.String(), e.in.K, topIDs(e, ev.Ranking, e.in.K), tiesString(e, ev))
		if idx < len(tl.Segments) {
			seg := tl.Segments[idx]
			fmt.Fprintf(w, "segment (%s, %s)  top-%d: %s   [rational sample t=%s]\n",
				seg.Lo.ExactString(), seg.Hi.ExactString(),
				e.in.K, topIDs(e, seg.Ranking, e.in.K), ratStr(seg.Sample))
		}
	}
}

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 && os.Args[1] != "-" {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		defer f.Close()
		r = f
	}
	var ji jsonInput
	if err := json.NewDecoder(r).Decode(&ji); err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid JSON input:", err)
		os.Exit(2)
	}
	in, err := ji.toInput()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	e := NewEngine(in)
	tl := e.ComputeTimeline()
	printTimeline(os.Stdout, e, tl)
	if err := e.Verify(tl); err != nil {
		fmt.Fprintln(os.Stderr, "VERIFICATION FAILED:", err)
		os.Exit(1)
	}
	fmt.Printf("verify: OK (%d segments, %d events, %d objects)\n",
		len(tl.Segments), len(tl.Events), e.n)
}
