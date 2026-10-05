package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

type placedItem struct {
	ID       string `json:"id"`
	Weight   string `json:"weight"`
	Slot     int    `json:"slot"`
	Position string `json:"position"`
}

type binOut struct {
	Bin             int          `json:"bin"`
	TotalWeight     string       `json:"total_weight"`
	CenterOfGravity string       `json:"center_of_gravity"`
	Eccentricity    string       `json:"eccentricity"`
	Items           []placedItem `json:"items"`
}

type output struct {
	Bins            int          `json:"bins"`
	MaxEccentricity string       `json:"max_eccentricity"`
	Target          string       `json:"target"`
	SafeInterval    []string     `json:"safe_interval"`
	Placement       []binOut     `json:"placement"`
	Verification    verifyReport `json:"verification"`
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "binsolve: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	var data []byte
	var err error
	if len(os.Args) > 2 {
		fmt.Fprintf(os.Stderr, "usage: binsolve [input.json]  (reads stdin when no file given)\n")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		data, err = ioutil.ReadFile(os.Args[1])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fatal("reading input: %v", err)
	}
	in, err := parseInstance(data)
	if err != nil {
		fatal("invalid instance: %v", err)
	}

	sol := solve(in)
	if sol == nil {
		fatal("no feasible placement exists")
	}
	rep := verify(in, sol)
	if !rep.OK {
		enc := json.NewEncoder(os.Stderr)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
		fatal("internal error: solution failed verification")
	}

	out := output{
		Bins:            len(sol.bins),
		MaxEccentricity: ratStr(sol.maxEcc),
		Target:          ratStr(in.Target),
		SafeInterval:    []string{ratStr(in.SafeLo), ratStr(in.SafeHi)},
		Verification:    rep,
	}
	for bi, bin := range sol.bins {
		bo := binOut{
			Bin:             bi,
			TotalWeight:     ratStr(bin.weight),
			CenterOfGravity: ratStr(bin.cog),
			Eccentricity:    ratStr(bin.ecc),
		}
		for k, it := range bin.items {
			bo.Items = append(bo.Items, placedItem{
				ID:       in.Items[it].ID,
				Weight:   ratStr(in.Items[it].Weight),
				Slot:     bin.slots[k],
				Position: ratStr(in.Positions[bin.slots[k]]),
			})
		}
		out.Placement = append(out.Placement, bo)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal("encoding output: %v", err)
	}
}
