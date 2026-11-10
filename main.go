package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

type input struct {
	InitialCapacity int     `json:"initial_capacity"`
	MaxEntries      int     `json:"max_entries"`
	Events          []Event `json:"events"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("qpack-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "", "path to JSON event input; defaults to stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var reader io.Reader = stdin
	if *path != "" {
		file, err := os.Open(*path)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}

	var payload input
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return err
	}

	replayer := NewReplayer(payload.InitialCapacity, payload.MaxEntries)
	result, err := replayer.Run(payload.Events)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
