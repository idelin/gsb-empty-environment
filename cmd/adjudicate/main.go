// Command adjudicate is the verification component for the compressed status
// list limit. It imports the netsec decoder and lets it make the ruling on a
// given framed input.
//
// Usage:
//
//	adjudicate <path-to-frame>
//
// Exit status 0 means the frame decoded to a readable bitmap; exit status 1
// means the decoder rejected it (the reason is printed to stderr).
package main

import (
	"errors"
	"fmt"
	"os"

	netsec "example.org/netsec/q0118"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s <path-to-frame>\n", os.Args[0])
		os.Exit(2)
	}

	frame, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: cannot read input: %v\n", err)
		os.Exit(1)
	}

	bitmap, err := netsec.DecodeStatusList(frame)
	if err != nil {
		switch {
		case errors.Is(err, netsec.ErrDeclaredTooLarge):
			fmt.Fprintf(os.Stderr, "REJECT: declared uncompressed length exceeds limit (%d bytes)\n", netsec.MaxDeclaredSize)
		case errors.Is(err, netsec.ErrLengthMismatch):
			fmt.Fprintf(os.Stderr, "REJECT: decompressed length does not match declared length\n")
		case errors.Is(err, netsec.ErrShortFrame):
			fmt.Fprintf(os.Stderr, "REJECT: frame too short for declared-length prefix\n")
		default:
			fmt.Fprintf(os.Stderr, "REJECT: malformed status list: %v\n", err)
		}
		os.Exit(1)
	}

	// A small bitmap must be readable after a successful decode.
	bit, bitErr := netsec.BitAt(bitmap, 0)
	if bitErr != nil {
		fmt.Fprintf(os.Stderr, "REJECT: bitmap not readable: %v\n", bitErr)
		os.Exit(1)
	}

	fmt.Printf("ACCEPT: decoded %d bitmap bytes; status[0]=%d\n", len(bitmap), bit)
	os.Exit(0)
}
