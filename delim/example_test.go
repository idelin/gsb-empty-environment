package delim_test

import (
	"fmt"

	"delim"
)

// An importer feeds the parser in chunks as data arrives, for example
// from a network stream, and consumes records as they complete.
func Example() {
	p := delim.New(delim.Config{MaxFieldSize: 16})

	chunks := []string{
		"name,not",          // record split across chunks
		"es\n\"multi\nline", // quoted newline
		"\",ok\nbad\"quote,rest of line\nafter,error\n",
	}
	for _, c := range chunks {
		for _, r := range p.Feed([]byte(c)) {
			print(r)
		}
	}
	for _, r := range p.Close() {
		print(r)
	}

	// Output:
	// record [0,11): ["name" "notes"]
	// record [11,27): ["multi\nline" "ok"]
	// record [27,50): illegal quote at [30,31) line 4: ["bad"]
	// record [50,62): ["after" "error"]
}

func print(r delim.Record) {
	if r.Err != nil {
		fmt.Printf("record [%d,%d): %s at [%d,%d) line %d: [%s]\n",
			r.Start, r.End, r.Err.Kind, r.Err.Start, r.Err.End, r.Err.Line,
			values(r))
		return
	}
	fmt.Printf("record [%d,%d): [%s]\n", r.Start, r.End, values(r))
}

func values(r delim.Record) (out string) {
	for i, f := range r.Fields {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%q", f.Value)
	}
	return
}
