package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"example.org/offlinecli/name"
)

type inputLine struct {
	Hex string `json:"hex"`
	At  []int  `json:"at"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		out, err := processLine(line)
		if err != nil {
			out = nil
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			encoded = []byte("null")
		}
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			return
		}
	}
}

// processLine turns one request line into the per-offset result array.
// A malformed line yields an error so the caller can emit null.
func processLine(line []byte) (any, error) {
	var in inputLine
	if err := json.Unmarshal(line, &in); err != nil {
		return nil, err
	}

	results := make([]any, len(in.At))
	packet, err := hex.DecodeString(stripSpaces(in.Hex))
	if err != nil {
		return results, nil
	}

	dec := name.New(packet)
	for i, offset := range in.At {
		labels, _, nextAt, err := dec.Resolve(offset)
		if err != nil {
			results[i] = nil
			continue
		}
		encoded := make([][]string, 0, len(labels))
		for _, label := range labels {
			encoded = append(encoded, labelHex(label))
		}
		results[i] = []any{encoded, nextAt}
	}
	return results, nil
}

func stripSpaces(s string) string {
	clean := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			clean = append(clean, s[i])
		}
	}
	return string(clean)
}

func labelHex(label []byte) []string {
	out := make([]string, len(label))
	for i, b := range label {
		out[i] = fmt.Sprintf("%02x", b)
	}
	return out
}
