// Command algcmp compares two real algebraic numbers exactly and
// produces (or independently verifies) a checkable certificate.
//
// Usage:
//
//	algcmp compare [input.json]   # prints {"result": ..., "certificate": ...}
//	algcmp verify  [input.json]   # input also carries "certificate"; prints {"valid": true}
//
// Input is read from the named file or from standard input.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"algcmp"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  algcmp compare [input.json]   compare two algebraic numbers, emit result + certificate
  algcmp verify  [input.json]   independently verify a certificate

input JSON:
  {"left":  {"poly": ["-2","0","1"], "lower": "1",   "upper": "2"},
   "right": {"poly": ["-3","0","1"], "lower": "3/2", "upper": "2"},
   "certificate": { ... }}        # only for verify

"poly" lists integer coefficients in ascending degree order; "lower" and
"upper" are rational endpoints of an open isolating interval containing
exactly one real root (endpoints must not be roots).
`)
	os.Exit(2)
}

func fail(err error) {
	out, _ := json.Marshal(map[string]string{"error": err.Error()})
	fmt.Fprintln(os.Stderr, string(out))
	os.Exit(1)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd := os.Args[1]
	var data []byte
	var err error
	if len(os.Args) > 2 {
		data, err = ioutil.ReadFile(os.Args[2])
	} else {
		data, err = ioutil.ReadAll(os.Stdin)
	}
	if err != nil {
		fail(err)
	}
	var doc algcmp.Doc
	if err := json.Unmarshal(data, &doc); err != nil {
		fail(fmt.Errorf("invalid JSON input: %v", err))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	switch cmd {
	case "compare":
		result, cert, err := algcmp.CompareJSON(doc.Left, doc.Right)
		if err != nil {
			fail(err)
		}
		enc.Encode(map[string]interface{}{"result": result, "certificate": cert})
	case "verify":
		if err := algcmp.Verify(doc); err != nil {
			fail(err)
		}
		enc.Encode(map[string]bool{"valid": true})
	default:
		usage()
	}
}
