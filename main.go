package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
)

// graphJSON is the input file format.
type graphJSON struct {
	Switches []string `json:"switches"`
	Edges    []struct {
		From string `json:"from"`
		To   string `json:"to"`
		When string `json:"when"`
	} `json:"edges"`
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: cyclecheck <graph.json>   (use - for stdin)\n")
	fmt.Fprintf(os.Stderr, `input format:
{
  "switches": ["x", "y"],           // at most 10 switch names
  "edges": [
    {"from": "A", "to": "B", "when": "x && !y"},  // "when" optional; empty = always
    {"from": "B", "to": "A", "when": "y"}
  ]
}
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) != 2 {
		usage()
	}
	var data []byte
	var err error
	if os.Args[1] == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error reading input:", err)
		os.Exit(1)
	}
	var gj graphJSON
	if err := json.Unmarshal(data, &gj); err != nil {
		fmt.Fprintln(os.Stderr, "error parsing JSON:", err)
		os.Exit(1)
	}
	g := Graph{Switches: gj.Switches}
	for _, e := range gj.Edges {
		g.Edges = append(g.Edges, Edge{From: e.From, To: e.To, When: e.When})
	}

	res, err := Check(g)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if res.Cyclic {
		fmt.Println("cyclic configuration found")
		parts := make([]string, len(g.Switches))
		for i, s := range g.Switches {
			v := "0"
			if res.BestConfig[i] {
				v = "1"
			}
			parts[i] = s + "=" + v
		}
		fmt.Printf("config: %s (%s)\n", BitString(res.BestConfig), strings.Join(parts, " "))
		fmt.Printf("cycle: %s\n", strings.Join(res.Cycle, " -> "))
	} else {
		fmt.Println("no configuration produces a cycle")
		fmt.Printf("configurations enumerated: %d\n", res.TotalConfigs)
	}
}
