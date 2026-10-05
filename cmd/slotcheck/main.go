// slotcheck is an independent slot validator: it reads a raw slot image
// file and reports whether it is a fully committed, intact slot.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"configstore"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: slotcheck <slot-file>\n")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "slotcheck: %v\n", err)
		os.Exit(2)
	}
	meta, payload, err := configstore.ValidateSlot(data)
	if err != nil {
		fmt.Printf("INVALID: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("VALID: generation=%d length=%d payload=%q\n", meta.Generation, meta.Length, payload)
}
