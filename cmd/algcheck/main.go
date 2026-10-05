// Command algcheck is the standalone certificate checker. It reads
// {"request": ..., "certificate": ...} as JSON from stdin and verifies the
// certificate independently of the comparator, printing {"valid": true}
// or {"valid": false, "error": "..."}.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"algcmp/algnum"
)

func main() {
	var cr algnum.CheckRequest
	if err := json.NewDecoder(os.Stdin).Decode(&cr); err != nil {
		fmt.Fprintf(os.Stderr, "decoding check request: %v\n", err)
		os.Exit(2)
	}
	a, b, err := cr.Request.Parse()
	if err == nil {
		err = algnum.Verify(a, b, cr.Certificate)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err != nil {
		enc.Encode(algnum.CheckResponse{Valid: false, Error: err.Error()})
		os.Exit(1)
	}
	enc.Encode(algnum.CheckResponse{Valid: true})
}
