// Command cesu8check adjudicates input bytes with Go's strict UTF-8 decoder.
//
// By default it reads raw bytes from standard input. With -hex it accepts a
// whitespace-separated hexadecimal string. Invalid input exits with status 1.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"example.org/netsec/q0321"
)

func main() {
	hexInput := flag.String("hex", "", "hexadecimal bytes, e.g. 'ed a0 bd ed b8 80'")
	flag.Parse()

	var (
		data []byte
		err  error
	)
	if *hexInput != "" {
		data, err = parseHex(*hexInput)
	} else {
		data, err = io.ReadAll(os.Stdin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	valid := netsec.Valid(data)
	fmt.Printf("valid: %t\n", valid)
	fmt.Printf("cesu8: %t\n", netsec.ContainsCESU8(data))
	fmt.Printf("cesu8_pair: %t\n", netsec.IsCESU8Pair(data))

	runes := netsec.Decode(data)
	fmt.Printf("runes:")
	for _, r := range runes {
		if r == utf8.RuneError {
			fmt.Printf(" U+FFFD")
			continue
		}
		fmt.Printf(" %U", r)
	}
	fmt.Println()

	if !valid {
		os.Exit(1)
	}
}

func parseHex(s string) ([]byte, error) {
	s = strings.Join(strings.Fields(s), "")
	return hex.DecodeString(s)
}
