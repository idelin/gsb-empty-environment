// Command deltaupdate is a file delta-update tool. The server side splits
// the new file into content-addressed blocks (split); the client builds a
// reuse/download plan against its old file (plan) and applies it with
// crash recovery (apply).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	delta "deltaupdate"
)

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  deltaupdate split  -block N -out DIR FILE      write manifest.json + block files to DIR
  deltaupdate plan   MANIFEST_JSON OLD_FILE      print update plan JSON to stdout
  deltaupdate apply  PLAN_JSON OLD_FILE NEW_FILE -blocks DIR
                                                 apply plan, resuming after interruption
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "split":
		err = cmdSplit(os.Args[2:])
	case "plan":
		err = cmdPlan(os.Args[2:])
	case "apply":
		err = cmdApply(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deltaupdate:", err)
		os.Exit(1)
	}
}

func cmdSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ExitOnError)
	block := fs.Int("block", delta.DefaultBlockSize, "block size in bytes")
	out := fs.String("out", "", "output directory for manifest.json and blocks")
	fs.Parse(args)
	if fs.NArg() != 1 || *out == "" {
		usage()
	}
	data, err := ioutil.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	m, err := delta.BuildManifest(bytes.NewReader(data), *block)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0755); err != nil {
		return err
	}
	for _, b := range m.Blocks {
		name := filepath.Join(*out, fmt.Sprintf("%06d.blk", b.Index))
		if err := ioutil.WriteFile(name, data[b.Offset:b.Offset+int64(b.Length)], 0644); err != nil {
			return err
		}
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(*out, "manifest.json"), mj, 0644)
}

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 2 {
		usage()
	}
	mj, err := ioutil.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var m delta.Manifest
	if err := json.Unmarshal(mj, &m); err != nil {
		return err
	}
	old, err := ioutil.ReadFile(fs.Arg(1))
	if err != nil {
		return err
	}
	plan := delta.BuildPlan(&m, old)
	pj, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(pj)
	return err
}

type dirSource string

func (d dirSource) FetchBlock(i int) ([]byte, error) {
	return ioutil.ReadFile(filepath.Join(string(d), fmt.Sprintf("%06d.blk", i)))
}

func cmdApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	blocks := fs.String("blocks", "", "directory containing server block files")
	fs.Parse(args)
	if fs.NArg() != 3 || *blocks == "" {
		usage()
	}
	pj, err := ioutil.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	var plan delta.Plan
	if err := json.Unmarshal(pj, &plan); err != nil {
		return err
	}
	return delta.Apply(&plan, fs.Arg(1), fs.Arg(2), &delta.Options{Source: dirSource(*blocks)})
}
