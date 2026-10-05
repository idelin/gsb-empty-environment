package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

type jsonGraph struct {
	Symbols []Symbol `json:"symbols"`
	Edges   []Edge   `json:"edges"`
}

type input struct {
	Old      jsonGraph `json:"old"`
	New      jsonGraph `json:"new"`
	Modified []string  `json:"modified"` // symbols whose body changed invisibly
}

type output struct {
	Changes  *ChangeSet  `json:"changes"`
	Selected []Selection `json:"selected"`
}

func buildGraph(jg jsonGraph) *Graph {
	g := NewGraph()
	for _, s := range jg.Symbols {
		g.AddSymbol(s)
	}
	for _, e := range jg.Edges {
		g.AddEdge(e)
	}
	return g
}

func main() {
	inFile := flag.String("input", "", "JSON input file (default: stdin)")
	flag.Parse()

	var data []byte
	var err error
	if *inFile == "" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(*inFile)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "read input:", err)
		os.Exit(1)
	}

	var in input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintln(os.Stderr, "parse input:", err)
		os.Exit(1)
	}

	oldG, newG := buildGraph(in.Old), buildGraph(in.New)
	changes := Diff(oldG, newG, in.Modified)
	selected := Select(oldG, newG, changes)
	if selected == nil {
		selected = []Selection{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(output{Changes: changes, Selected: selected}); err != nil {
		fmt.Fprintln(os.Stderr, "encode output:", err)
		os.Exit(1)
	}
}
