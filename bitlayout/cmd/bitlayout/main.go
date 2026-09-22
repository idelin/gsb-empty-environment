// Command bitlayout compiles a JSON record-layout schema and encodes or
// decodes records with it.
//
// Usage:
//
//	bitlayout check  <schema.json>
//	bitlayout encode <schema.json> <object.json>
//	bitlayout decode <schema.json> <hex>
package main

import (
	"bytes"
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
	var err error
	switch os.Args[1] {
	case "check":
		err = cmdCheck(os.Args[2])
	case "encode":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		err = cmdEncode(os.Args[2], os.Args[3])
	case "decode":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		err = cmdDecode(os.Args[2], os.Args[3])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  bitlayout check  <schema.json>            compile and print plan stats
  bitlayout encode <schema.json> <obj.json> encode object, print hex
  bitlayout decode <schema.json> <hex>      decode hex record, print JSON`)
}

func cmdCheck(schemaPath string) error {
	p, err := bitlayout.LoadSchema(schemaPath)
	if err != nil {
		return err
	}
	fmt.Printf("plan %q: %d top-level fields, %d decode ops, max %d bits (%d bytes)\n",
		p.Name, p.NumFields(), p.NumOps(), p.MaxBits(), (p.MaxBits()+7)/8)
	return nil
}

func cmdEncode(schemaPath, objPath string) error {
	p, err := bitlayout.LoadSchema(schemaPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(objPath)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("parsing object: %w", err)
	}
	buf, nbits, err := p.EncodeBits(obj)
	if err != nil {
		return err
	}
	fmt.Printf("%s  (%d bits, %d bytes)\n", hex.EncodeToString(buf), nbits, len(buf))
	return nil
}

func cmdDecode(schemaPath, hexData string) error {
	p, err := bitlayout.LoadSchema(schemaPath)
	if err != nil {
		return err
	}
	data, err := hex.DecodeString(hexData)
	if err != nil {
		return fmt.Errorf("bad hex: %w", err)
	}
	s := p.NewSession(bitlayout.Quotas{})
	s.Feed(data)
	if err := s.Run(); err != nil {
		return err
	}
	rec, used, ok := s.Result()
	if !ok {
		return fmt.Errorf("no record delivered")
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n(%d bits consumed of %d fed)\n", out, used, len(data)*8)
	return nil
}
