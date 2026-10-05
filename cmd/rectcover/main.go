// Command rectcover reads a rectangle cover problem as JSON on stdin and
// writes the exact solution as JSON on stdout.
//
// Input:
//   {"targets": [[x,y],...], "forbidden": [[x,y],...],
//    "maxArea": 0, "maxRects": 0, "maxNodes": 0}
//
// Output:
//   {"status": "optimal", "area": 2,
//    "rects": [{"minX":0,"minY":0,"maxX":0,"maxY":0}, ...],
//    "evidence": [{"target": [x,y], "rect": 0}, ...],
//    "nodes": 12}
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"rectcover"
)

type input struct {
	Targets   [][]int `json:"targets"`
	Forbidden [][]int `json:"forbidden"`
	MaxArea   int     `json:"maxArea"`
	MaxRects  int     `json:"maxRects"`
	MaxNodes  int64   `json:"maxNodes"`
}

type rectJSON struct {
	MinX int `json:"minX"`
	MinY int `json:"minY"`
	MaxX int `json:"maxX"`
	MaxY int `json:"maxY"`
}

type evidenceJSON struct {
	Target []int `json:"target"`
	Rect   int   `json:"rect"`
}

type output struct {
	Status   string         `json:"status"`
	Area     int            `json:"area"`
	Rects    []rectJSON     `json:"rects,omitempty"`
	Evidence []evidenceJSON `json:"evidence,omitempty"`
	Nodes    int64          `json:"nodes"`
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "rectcover:", err)
	os.Exit(2)
}

func toPoints(pairs [][]int, what string) ([]rectcover.Point, error) {
	pts := make([]rectcover.Point, 0, len(pairs))
	for _, pair := range pairs {
		if len(pair) != 2 {
			return nil, fmt.Errorf("%s point %v is not an [x,y] pair", what, pair)
		}
		pts = append(pts, rectcover.Point{X: pair[0], Y: pair[1]})
	}
	return pts, nil
}

func main() {
	var in input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		fail(err)
	}
	targets, err := toPoints(in.Targets, "target")
	if err != nil {
		fail(err)
	}
	forbidden, err := toPoints(in.Forbidden, "forbidden")
	if err != nil {
		fail(err)
	}
	sol := rectcover.Solve(rectcover.Problem{Targets: targets, Forbidden: forbidden},
		rectcover.Options{MaxArea: in.MaxArea, MaxRects: in.MaxRects, MaxNodes: in.MaxNodes})

	out := output{Status: string(sol.Status), Area: sol.Area, Nodes: sol.Nodes}
	for _, r := range sol.Rects {
		out.Rects = append(out.Rects, rectJSON{r.MinX, r.MinY, r.MaxX, r.MaxY})
	}
	for _, e := range sol.Evidence {
		out.Evidence = append(out.Evidence, evidenceJSON{[]int{e.Target.X, e.Target.Y}, e.Rect})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail(err)
	}
}
