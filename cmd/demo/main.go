// Command demo builds a two-frame stream, feeds it through the streaming
// parser in awkward 7-byte chunks, redacts nested secrets, and prints the
// resulting frames to show lengths and CRCs still check out.
package main

import (
	"bytes"
	"fmt"
	"log"

	"example.com/sframe"
)

func main() {
	const (
		password = "s3cret-PA55word"
		token    = "tok-live-9f8e7d6c"
	)

	payload := sframe.MarshalObject([]*sframe.Field{
		{Name: "user", Type: sframe.TypeObject, Kids: []*sframe.Field{
			{Name: "name", Type: sframe.TypeString, Raw: []byte("alice")},
			{Name: "credentials", Type: sframe.TypeObject, Kids: []*sframe.Field{
				{Name: "password", Type: sframe.TypeString, Raw: []byte(password)},
				{Name: "token", Type: sframe.TypeString, Raw: []byte(token)},
			}},
		}},
		// Unknown extension field: parser preserves it verbatim.
		{Name: "x-ext-9f", Type: sframe.TypeBytes, Raw: []byte("ext-payload-bytes")},
		{Name: "attempt", Type: sframe.TypeInt64, Int: 3},
	})

	var stream []byte
	for i := uint64(1); i <= 2; i++ {
		hdr := sframe.Header{Version: sframe.Version1, Seq: i, Timestamp: 1_758_000_000_000_000_000 + int64(i)}
		stream = append(stream, sframe.EncodeFrame(hdr, payload)...)
	}

	parser := sframe.NewParser(1 << 20)
	redactor := &sframe.Redactor{
		Paths:  []string{"user.credentials.password", "user.credentials.token"},
		Strict: true,
	}

	fmt.Println("== redacted frames (fed to parser in 7-byte chunks) ==")
	var outStream []byte
	for off := 0; off < len(stream); off += 7 {
		end := off + 7
		if end > len(stream) {
			end = len(stream)
		}
		frames, err := parser.Feed(stream[off:end])
		if err != nil {
			log.Fatalf("parse: %v", err)
		}
		for _, f := range frames {
			out, err := redactor.Redact(f)
			if err != nil {
				log.Fatalf("redact: %v", err)
			}
			outStream = append(outStream, out...)
		}
	}

	// Re-parse the redacted stream: lengths and CRCs must hold.
	check := sframe.NewParser(1 << 20)
	frames, err := check.Feed(outStream)
	if err != nil {
		log.Fatalf("re-parse redacted stream: %v", err)
	}
	for _, f := range frames {
		hdr, payload, err := sframe.SplitFrame(f)
		if err != nil {
			log.Fatalf("split: %v", err)
		}
		fields, err := sframe.ParsePayload(payload)
		if err != nil {
			log.Fatalf("payload: %v", err)
		}
		fmt.Printf("frame seq=%d ts=%d bytes=%d\n%s", hdr.Seq, hdr.Timestamp, len(f), sframe.Dump(fields))
	}

	if bytes.Contains(outStream, []byte(password)) || bytes.Contains(outStream, []byte(token)) {
		log.Fatal("sensitive bytes leaked into output stream")
	}
	if !bytes.Contains(outStream, []byte("ext-payload-bytes")) {
		log.Fatal("unknown extension field was not preserved")
	}
	fmt.Printf("OK: %d frames redacted, secrets absent, extension preserved, CRCs valid\n", len(frames))
}
