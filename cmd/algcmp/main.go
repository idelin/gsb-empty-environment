// Command algcmp compares two real algebraic numbers read as JSON from
// stdin and prints the result with an independently checkable certificate.
//
// Input:
//
//	{"lhs": {"coeffs": ["-2","0","1"], "lo": "1", "hi": "3/2"},
//	 "rhs": {"coeffs": ["-3","0","1"], "lo": "1", "hi": "2"}}
//
// Output (one of "less", "equal", "greater"):
//
//	{"relation": "less", "certificate": {...}}
//
// With -check it instead reads {"request": ..., "certificate": ...} and
// independently verifies the certificate, printing {"valid": true}.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"algcmp/algnum"
)

func fail(err error) {
	json.NewEncoder(os.Stdout).Encode(algnum.Response{Error: err.Error()})
	os.Exit(2)
}

func main() {
	check := false
	for _, arg := range os.Args[1:] {
		if arg == "-check" || arg == "--check" {
			check = true
		}
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	if check {
		var cr algnum.CheckRequest
		if err := dec.Decode(&cr); err != nil {
			fmt.Fprintf(os.Stderr, "decoding check request: %v\n", err)
			os.Exit(2)
		}
		a, b, err := cr.Request.Parse()
		if err == nil {
			err = algnum.Verify(a, b, cr.Certificate)
		}
		if err != nil {
			enc.Encode(algnum.CheckResponse{Valid: false, Error: err.Error()})
			os.Exit(1)
		}
		enc.Encode(algnum.CheckResponse{Valid: true})
		return
	}

	var req algnum.Request
	if err := dec.Decode(&req); err != nil {
		fmt.Fprintf(os.Stderr, "decoding request: %v\n", err)
		os.Exit(2)
	}
	a, b, err := req.Parse()
	if err != nil {
		fail(err)
	}
	rel, cert, err := algnum.Compare(a, b)
	if err != nil {
		fail(err)
	}
	enc.Encode(algnum.Response{Relation: rel.String(), Certificate: &cert})
}
