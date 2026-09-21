// Command bitlayout compiles a JSON schema into a reusable plan and can
// encode/decode records with it.
//
//	bitlayout compile schema.json
//	bitlayout encode  schema.json record.json
//	bitlayout decode  schema.json <hex>
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"bitlayout"
)

func main() {
	if len(os.Args) < 3 {
		usage()
		os.Exit(2)
	}
	cmd, schemaPath := os.Args[1], os.Args[2]
	data, err := os.ReadFile(schemaPath)
	must(err)
	plan, err := bitlayout.Compile(data, nil)
	must(err)

	switch cmd {
	case "compile":
		c := plan.Cost()
		out, _ := json.MarshalIndent(map[string]any{
			"name":          plan.Name,
			"bitOrder":      plan.BitOrder,
			"byteOrder":     plan.ByteOrder,
			"fields":        c.Fields,
			"maxDepth":      c.MaxDepth,
			"minRecordBits": c.MinRecordBits,
			"maxRecordBits": c.MaxRecordBits,
			"maxElements":   c.MaxElements,
			"checksumBits":  c.ChecksumBits,
		}, "", "  ")
		fmt.Println(string(out))

	case "encode":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		recJSON, err := os.ReadFile(os.Args[3])
		must(err)
		rec, err := plan.UnmarshalRecord(recJSON)
		must(err)
		nbits, err := plan.Validate(rec)
		must(err)
		buf := make([]byte, (nbits+7)/8)
		n, err := plan.Encode(rec, buf)
		must(err)
		fmt.Printf("%d bits: %s\n", n, hex.EncodeToString(buf))

	case "decode":
		if len(os.Args) < 5 {
			usage()
			os.Exit(2)
		}
		raw, err := hex.DecodeString(os.Args[3])
		must(err)
		var nbits int64
		_, err = fmt.Sscan(os.Args[4], &nbits)
		must(err)
		rec, n, err := plan.Decode(raw, nbits, nil)
		must(err)
		out, _ := json.MarshalIndent(rec, "", "  ")
		fmt.Printf("consumed %d bits:\n%s\n", n, out)

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  bitlayout compile schema.json
  bitlayout encode  schema.json record.json
  bitlayout decode  schema.json <hex> <nbits>
`)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
