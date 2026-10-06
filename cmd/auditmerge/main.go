// Command auditmerge merges hash-chained audit log replicas and prints the
// longest common trusted prefix as JSON. Fork and tamper evidence goes to
// stderr; the exit code is 2 when manual resolution is required.
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"auditmerge/auditlog"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <replica.json> [replica.json ...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "each file: {\"name\": \"...\", \"entries\": [{\"seq\":1,\"payload\":\"...\",\"prev_hash\":\"<hex>\",\"hash\":\"<hex>\"}, ...]}\n")
		os.Exit(64)
	}

	var sources []auditlog.Source
	for _, path := range os.Args[1:] {
		raw, err := ioutil.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
			os.Exit(66)
		}
		var src auditlog.Source
		if err := json.Unmarshal(raw, &src); err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", path, err)
			os.Exit(65)
		}
		if src.Name == "" {
			src.Name = path
		}
		sources = append(sources, src)
	}

	res := auditlog.Merge(sources)
	fmt.Fprintln(os.Stderr, res.Summary())

	out, err := json.MarshalIndent(res.TrustedPrefix, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(70)
	}
	fmt.Println(string(out))

	if res.StopReason == auditlog.StopFork {
		os.Exit(2) // divergence: evidence kept, no side selected
	}
}
