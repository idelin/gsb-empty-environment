package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

type inputDoc struct {
	Fragments []fragment `json:"fragments"`
}

func main() {
	inPath := flag.String("in", "", "path to the JSON input document (defaults to stdin)")
	outPath := flag.String("out", "", "path to write the report (defaults to stdout)")
	format := flag.String("format", "json", "report format: json or text")
	flag.Parse()

	raw, err := readInput(*inPath)
	if err != nil {
		fatal(err)
	}

	var doc inputDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		fatal(fmt.Errorf("decode input: %w", err))
	}

	report, err := analyze(doc.Fragments)
	if err != nil {
		fatal(err)
	}

	out, err := render(report, *format)
	if err != nil {
		fatal(err)
	}
	if err := writeOutput(*outPath, out); err != nil {
		fatal(err)
	}
}

func readInput(path string) ([]byte, error) {
	if path == "" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func writeOutput(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "keystream-audit:", err)
	os.Exit(1)
}
