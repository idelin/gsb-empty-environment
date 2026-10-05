package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

type cutOut struct {
	Nodes    []string `json:"nodes"`
	Capacity int64    `json:"capacity"`
}

type verificationOut struct {
	PerCutCapacity string `json:"per_cut_capacity"`
	BruteForce     string `json:"brute_force"`
}

type pageOut struct {
	GraphVersion string          `json:"graph_version"`
	MaxFlow      int64           `json:"max_flow"`
	Offset       int             `json:"offset"`
	PageSize     int             `json:"page_size"`
	KnownTotal   int             `json:"known_total"`
	Complete     bool            `json:"complete"`
	NextCursor   string          `json:"next_cursor,omitempty"`
	Cuts         []cutOut        `json:"cuts"`
	Verification verificationOut `json:"verification"`
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	graphPath := flag.String("graph", "-", "path to the graph JSON file, or - for stdin")
	pageSize := flag.Int("page-size", 50, "number of cuts per page")
	cursorStr := flag.String("cursor", "", "pagination cursor from a previous response")
	budget := flag.Int("budget", 100000, "maximum number of closed sets generated during enumeration (-1 for unlimited)")
	bruteThreshold := flag.Int("brute-threshold", 20, "exhaustively recheck all subsets up to this many nodes (0 disables)")
	flag.Parse()

	if *pageSize <= 0 {
		fail(fmt.Errorf("page-size must be positive"))
	}

	var raw []byte
	var err error
	if *graphPath == "-" {
		raw, err = ioutil.ReadAll(os.Stdin)
	} else {
		raw, err = ioutil.ReadFile(*graphPath)
	}
	if err != nil {
		fail(err)
	}
	var g Graph
	if err := json.Unmarshal(raw, &g); err != nil {
		fail(fmt.Errorf("parsing graph: %v", err))
	}

	res, err := solve(&g, *budget)
	if err != nil {
		fail(err)
	}

	out := buildPage(&g, res, *pageSize, *cursorStr, *bruteThreshold)
	if out == nil {
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail(err)
	}
	if out.Verification.PerCutCapacity != "ok" || (out.Verification.BruteForce != "ok" && out.Verification.BruteForce != "skipped") {
		os.Exit(2)
	}
}

// buildPage slices the requested page out of the solved cut list and runs
// the independent verifications. It returns nil after printing an error.
func buildPage(g *Graph, res *SolveResult, pageSize int, cursorStr string, bruteThreshold int) *pageOut {
	offset := 0
	if cursorStr != "" {
		c, err := decodeCursor(cursorStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return nil
		}
		if c.Version != res.Version {
			fmt.Fprintf(os.Stderr, "error: cursor is bound to graph version %q but the current graph version is %q\n", c.Version, res.Version)
			return nil
		}
		offset = c.Offset
	}
	if offset > len(res.Cuts) {
		fmt.Fprintf(os.Stderr, "error: cursor offset %d exceeds the %d known cuts\n", offset, len(res.Cuts))
		return nil
	}

	end := offset + pageSize
	if end > len(res.Cuts) {
		end = len(res.Cuts)
	}
	page := res.Cuts[offset:end]

	out := &pageOut{
		GraphVersion: res.Version,
		MaxFlow:      res.MaxFlow,
		Offset:       offset,
		PageSize:     pageSize,
		KnownTotal:   len(res.Cuts),
		Complete:     res.Complete,
		Cuts:         make([]cutOut, 0, len(page)),
	}
	if end < len(res.Cuts) {
		out.NextCursor = encodeCursor(res.Version, end)
	}
	for _, cut := range page {
		out.Cuts = append(out.Cuts, cutOut{Nodes: cut, Capacity: cutCapacity(g, cut)})
	}

	out.Verification.PerCutCapacity = "ok"
	if err := verifyCutCapacities(g, res.MaxFlow, res.Cuts); err != nil {
		out.Verification.PerCutCapacity = "failed: " + err.Error()
	}
	out.Verification.BruteForce = "skipped"
	if bruteThreshold > 0 && len(g.NodeList()) <= bruteThreshold {
		if err := bruteForceCheck(g, res); err != nil {
			out.Verification.BruteForce = "failed: " + err.Error()
		} else {
			out.Verification.BruteForce = "ok"
		}
	}
	return out
}
