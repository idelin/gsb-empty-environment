package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type inputLine struct {
	Hex string `json:"hex"`
	At  []int  `json:"at"`
}

func main() {
	if err := runMain(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runMain reads one JSON object per input line and writes one JSON array per
// output line. A malformed line emits null and is reported on the error stream
// without aborting the rest of the batch.
func runMain(in io.Reader, out, errOut io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 256*1024*1024)
	w := bufio.NewWriter(out)
	defer w.Flush()

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}

		var line inputLine
		if err := json.Unmarshal(raw, &line); err != nil {
			fmt.Fprintf(errOut, "line %d: invalid JSON: %v\n", lineNo, err)
			w.WriteString("null\n")
			continue
		}

		packet, err := hex.DecodeString(string(bytes.TrimSpace([]byte(line.Hex))))
		if err != nil {
			fmt.Fprintf(errOut, "line %d: invalid hex: %v\n", lineNo, err)
			w.WriteString("null\n")
			continue
		}

		encoded, err := json.Marshal(decodePacket(packet, line.At))
		if err != nil {
			fmt.Fprintf(errOut, "line %d: marshal failed: %v\n", lineNo, err)
			w.WriteString("null\n")
			continue
		}
		w.Write(encoded)
		w.WriteByte('\n')
	}
	return scanner.Err()
}
