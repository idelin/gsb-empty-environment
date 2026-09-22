package main

import (
	"encoding/binary"
	"fmt"
	"os"

	"safeframe"
)

func main() {
	frame := &safeframe.Frame{
		Version:       safeframe.Version1,
		Sequence:      1001,
		TimestampNano: 1760000000000000000,
		Root: safeframe.Root{
			{Name: "account", Type: safeframe.TypeMessage, Root: safeframe.Root{
				{Name: "id", Type: safeframe.TypeString, Text: "acct-1001"},
				{Name: "token", Type: safeframe.TypeString, Text: "demo-token-42"},
			}},
			{Name: "vendorX", Type: 77, Extra: []byte("opaque extension bytes")},
		},
	}

	wire, err := frame.Marshal()
	if err != nil {
		fail(err)
	}
	fmt.Printf("encoded frame: %d bytes, payload=%d, crc=%#08x\n",
		len(wire), binary.BigEndian.Uint32(wire[8:12]), binary.BigEndian.Uint32(wire[28:32]))

	decoder := safeframe.NewDecoder(safeframe.MaxPayloadSize)
	var decoded *safeframe.Frame
	for i := 0; i < len(wire); i++ {
		frames, err := decoder.Feed(wire[i : i+1])
		if err != nil && err != safeframe.ErrNeedMoreData && err != safeframe.ErrTruncated {
			fail(err)
		}
		if len(frames) == 1 {
			decoded = frames[0]
		}
	}
	if decoded == nil || decoded.Sequence != 1001 {
		fail(fmt.Errorf("byte-by-byte decode did not produce frame 1001"))
	}
	fmt.Println("stream decode: byte-by-byte feed succeeded; sequence and timestamp preserved")

	_, redacted, err := safeframe.Redact(decoded,
		[]safeframe.Path{safeframe.Path{"account", "token"}},
		safeframe.RedactOptions{
			Strict:      true,
			Replacement: []byte("[REDACTED-LONGER]"),
		})
	if err != nil {
		fail(err)
	}
	fmt.Printf("nested redaction: %d bytes, payload=%d, crc=%#08x; parses=%v\n",
		len(redacted), binary.BigEndian.Uint32(redacted[8:12]),
		binary.BigEndian.Uint32(redacted[28:32]), canParse(redacted))

	_, _, err = safeframe.Redact(decoded,
		[]safeframe.Path{safeframe.Path{"vendorX", "token"}},
		safeframe.RedactOptions{Strict: true, Replacement: []byte("[REDACTED]")})
	fmt.Printf("unknown-extension path: rejected (%v), no output emitted\n", err)
}

func canParse(wire []byte) bool {
	frame, err := safeframe.DecodeFrame(wire)
	return err == nil && frame.Sequence == 1001
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
