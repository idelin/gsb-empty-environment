package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// internalError reports an inconsistency detected by the tool's own
// verification (exit code 2), as opposed to bad user input (exit code 1).
type internalError struct{ msg string }

func (e *internalError) Error() string { return e.msg }

type selfCheckReport struct {
	Method  string `json:"method"`
	Subsets int    `json:"subsets"`
	MinCuts int    `json:"min_cuts"`
	OK      bool   `json:"ok"`
}

type pageOutput struct {
	GraphVersion string           `json:"graph_version"`
	Vertices     int              `json:"vertices"`
	Source       int              `json:"source"`
	Sink         int              `json:"sink"`
	MaxFlow      int64            `json:"max_flow"`
	Limit        int              `json:"limit"`
	Count        int              `json:"count"`
	Cuts         [][]int          `json:"cuts"`
	Complete     bool             `json:"complete"`
	NextCursor   string           `json:"next_cursor,omitempty"`
	Verified     bool             `json:"verified"`
	SelfCheck    *selfCheckReport `json:"self_check,omitempty"`
}

// computePage computes the max flow and emits one page of minimum cuts in
// lexicographic order. Every emitted cut is independently re-checked to
// have capacity equal to the max-flow value. If the page budget (limit)
// is exhausted before enumeration ends, Complete is false and NextCursor
// resumes the listing; the tool never claims completeness in that case.
func computePage(g *Graph, limit int, cursorStr string) (*pageOutput, error) {
	if limit < 0 {
		return nil, fmt.Errorf("limit must be >= 0, got %d", limit)
	}
	gv := g.Version()
	var last []int
	if cursorStr != "" {
		c, err := decodeCursor(cursorStr)
		if err != nil {
			return nil, err
		}
		if c.GV != gv {
			return nil, fmt.Errorf(
				"cursor was issued for graph version %s but the current graph is %s; refusing to resume",
				c.GV, gv)
		}
		for i, v := range c.Last {
			if v < 1 || v > g.N {
				return nil, fmt.Errorf("cursor vertex %d out of range [1,%d]", v, g.N)
			}
			if i > 0 && v <= c.Last[i-1] {
				return nil, fmt.Errorf("cursor cut %v is not strictly increasing", c.Last)
			}
		}
		last = c.Last
	}

	maxFlow, residual := maxFlowValue(g)
	enum := NewEnumerator(residual, g.N, g.S, g.T)

	out := &pageOutput{
		GraphVersion: gv,
		Vertices:     g.N,
		Source:       g.S + 1,
		Sink:         g.T + 1,
		MaxFlow:      maxFlow,
		Limit:        limit,
		Cuts:         [][]int{},
	}
	hasMore := false
	var verifyErr error
	enum.Visit(func(cut []int) bool {
		if last != nil && lexCompare(cut, last) <= 0 {
			return true // already emitted in an earlier page
		}
		if limit > 0 && len(out.Cuts) == limit {
			hasMore = true // budget exhausted: more cuts remain unlisted
			return false
		}
		if cap := cutCapacity(g, cut); cap != maxFlow {
			verifyErr = &internalError{msg: fmt.Sprintf(
				"cut %v has capacity %d, expected max-flow value %d", cut, cap, maxFlow)}
			return false
		}
		out.Cuts = append(out.Cuts, cut)
		return true
	})
	if verifyErr != nil {
		return nil, verifyErr
	}
	out.Count = len(out.Cuts)
	out.Complete = !hasMore
	out.Verified = true
	if hasMore {
		out.NextCursor = encodeCursor(gv, out.Cuts[len(out.Cuts)-1])
	}
	return out, nil
}

// runSelfCheck recomputes everything by exhaustive subset enumeration and
// cross-checks both the max-flow value and the full cut list.
func runSelfCheck(g *Graph, maxFlow int64) (*selfCheckReport, error) {
	best, bruteCuts := bruteForceMinCuts(g)
	_, residual := maxFlowValue(g)
	enum := NewEnumerator(residual, g.N, g.S, g.T)
	var all [][]int
	enum.Visit(func(cut []int) bool {
		all = append(all, cut)
		return true
	})
	report := &selfCheckReport{
		Method:  "exhaustive-subset",
		Subsets: 1 << uint(g.N-2),
		MinCuts: len(bruteCuts),
	}
	if best != maxFlow {
		return report, &internalError{msg: fmt.Sprintf(
			"self-check failed: exhaustive min-cut capacity %d != max-flow value %d", best, maxFlow)}
	}
	if !equalCuts(bruteCuts, all) {
		return report, &internalError{msg: fmt.Sprintf(
			"self-check failed: enumerator produced %d cuts, exhaustive search found %d",
			len(all), len(bruteCuts))}
	}
	report.OK = true
	return report, nil
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("mincutenum", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	input := fs.String("i", "", "input file (default: stdin)")
	limit := fs.Int("limit", 100, "page size: max cuts per page; 0 means unlimited")
	cursor := fs.String("cursor", "", "pagination cursor from a previous page")
	selfCheckMax := fs.Int("selfcheck-max", 20,
		"exhaustively recompute all subsets when n <= this value; -1 disables")
	if err := fs.Parse(args); err != nil {
		usage(os.Stderr)
		return err
	}

	var r io.Reader = stdin
	if *input != "" {
		f, err := os.Open(*input)
		if err != nil {
			return fmt.Errorf("opening input: %w", err)
		}
		defer f.Close()
		r = f
	}
	g, err := ParseGraph(r)
	if err != nil {
		usage(os.Stderr)
		return err
	}

	out, err := computePage(g, *limit, *cursor)
	if err != nil {
		return err
	}
	var selfCheckErr error
	if *selfCheckMax >= 0 && g.N <= *selfCheckMax {
		out.SelfCheck, selfCheckErr = runSelfCheck(g, out.MaxFlow)
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}
	return selfCheckErr
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `mincutenum - enumerate all minimum s-t cuts of a directed network

Input format (1-based vertices, non-negative integer capacities):
  n m s t
  u1 v1 c1
  ...
  um vm cm

Output: JSON with the max-flow value and one page of minimum cuts,
each cut an ascending vertex list, pages in lexicographic order.
complete is true only when every minimum cut has been listed; when the
page budget is exhausted, complete is false and next_cursor resumes.
Cursors are bound to the graph version and rejected if the graph changes.`)
}

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	var ierr *internalError
	if errors.As(err, &ierr) {
		os.Exit(2)
	}
	os.Exit(1)
}
