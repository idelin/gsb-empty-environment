// Command delaunay reads a JSON point list from stdin and writes the
// canonical Delaunay triangulation as JSON to stdout.
//
// Input:  {"points":[{"id":"p0","x":0,"y":0}, ...]}
// Output: {"mode":...,"points":[...],"triangles":[...],"adjacency":[...],
//          "hull":[...],"valid":true}
//
// Coordinates are arbitrary-precision integers (JSON numbers). Duplicate
// coordinates are merged and their IDs retained. The output is canonical:
// it does not depend on the input order (see package doc for the public
// tie-break rule).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"delaunay"
)

func main() {
	var in delaunay.Input
	dec := json.NewDecoder(os.Stdin)
	dec.UseNumber()
	if err := dec.Decode(&in); err != nil {
		fmt.Fprintln(os.Stderr, "decode input:", err)
		os.Exit(2)
	}
	raw, err := in.Raw()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid input:", err)
		os.Exit(2)
	}
	out := delaunay.Triangulate(raw)
	if err := delaunay.Validate(raw, out); err != nil {
		fmt.Fprintln(os.Stderr, "validation failed:", err)
		os.Exit(1)
	}
	doc := struct {
		*delaunay.Output
		Valid bool `json:"valid"`
	}{out, true}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, "encode output:", err)
		os.Exit(2)
	}
}
