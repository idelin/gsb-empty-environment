package dkimcanon_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"example.org/offlinecli/dkimcanon"
)

func ExampleCanonicalize() {
	body := []byte("Hello: Foo\r\n\r\n")

	simple, _ := dkimcanon.Canonicalize(dkimcanon.Simple, body)
	relaxed, _ := dkimcanon.Canonicalize(dkimcanon.Relaxed, body)

	fmt.Printf("simple:  %q\n", simple)
	fmt.Printf("relaxed: %q\n", relaxed)
	// Output:
	// simple:  "Hello: Foo\r\n"
	// relaxed: "Hello: Foo\r\n"
}

// ExampleNewDigester verifies a body hash (the DKIM "bh=" tag) while
// streaming the body through arbitrary chunk sizes.
func ExampleNewDigester() {
	body := []byte("Subject: greeting\r\n\r\nhello\r\n\r\n\r\n")

	// Reference value a signer would have produced.
	canon, _ := dkimcanon.Canonicalize(dkimcanon.Relaxed, body)
	wantSum := sha256.Sum256(canon)

	// Chunk the body at arbitrary boundaries, including mid-CRLF.
	h := sha256.New()
	d := dkimcanon.NewDigester(dkimcanon.Relaxed, h)
	for pos := 0; pos < len(body); {
		end := pos + 3
		if end > len(body) {
			end = len(body)
		}
		if _, err := d.Write(body[pos:end]); err != nil {
			panic(err)
		}
		pos = end
	}

	if err := d.Verify(wantSum[:]); err != nil {
		panic(err)
	}
	fmt.Println("digest verified:", hex.EncodeToString(wantSum[:])[:12]+"...")
	// Output:
	// digest verified: 85396a5f2059...
}

// ExampleNewDigesterWithSink shows a relay forwarding canonical bytes while
// simultaneously checking the digest, and rejecting an illegal newline.
func ExampleNewDigesterWithSink() {
	goodBody := []byte("line one\r\nline two\r\n")

	var forwarded bytes.Buffer
	h := sha256.New()
	d := dkimcanon.NewDigesterWithSink(
		dkimcanon.Simple, h,
		dkimcanon.WriteFunc(func(p []byte) error {
			_, err := forwarded.Write(p)
			return err
		}),
	)
	if _, err := d.Write(goodBody); err != nil {
		panic(err)
	}
	sum, err := d.Sum()
	if err != nil {
		panic(err)
	}
	fmt.Printf("forwarded: %q\n", forwarded.Bytes())
	fmt.Println("digest bytes:", len(sum))

	// A body with a bare LF must never be delivered as verified.
	bad := dkimcanon.NewDigester(dkimcanon.Simple, sha256.New())
	if _, err := bad.Write([]byte("unix\nnewline\r\n")); err != nil {
		fmt.Println("rejected:", errors.Is(err, dkimcanon.ErrInvalidNewline))
	}
	// Output:
	// forwarded: "line one\r\nline two\r\n"
	// digest bytes: 32
	// rejected: true
}

func ExampleWithBodyLength() {
	body := []byte("alpha\r\nbeta\r\n")

	full, _ := dkimcanon.Canonicalize(dkimcanon.Simple, body)
	limited, _ := dkimcanon.Canonicalize(dkimcanon.Simple, body, dkimcanon.WithBodyLength(7))

	fmt.Printf("full:    %q\n", full)
	fmt.Printf("limited: %q\n", limited)
	// Output:
	// full:    "alpha\r\nbeta\r\n"
	// limited: "alpha\r\n"
}
