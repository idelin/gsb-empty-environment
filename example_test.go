package deflate_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"

	"deflate"
)

// ExampleCompress compresses repetitive system logs and verifies the
// round trip with SHA256.
func ExampleCompress() {
	var logs bytes.Buffer
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&logs, "2026-10-02T08:15:%02dZ INFO host=web-01 service=auth msg=\"request completed status=200\"\n", i%60)
	}
	data := logs.Bytes()

	compressed := deflate.Compress(data)
	restored, err := deflate.Decompress(compressed)
	if err != nil {
		panic(err)
	}

	fmt.Printf("original:   %d bytes\n", len(data))
	fmt.Printf("compressed: %d bytes (%.1f%%)\n", len(compressed), 100*float64(len(compressed))/float64(len(data)))
	fmt.Printf("sha256 match: %v\n", sha256.Sum256(data) == sha256.Sum256(restored))
	// Output:
	// original:   43000 bytes
	// compressed: 522 bytes (1.2%)
	// sha256 match: true
}
