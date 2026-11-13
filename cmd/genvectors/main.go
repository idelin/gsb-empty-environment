// Command genvectors regenerates the framed status list fixtures under
// testdata. It documents exactly how the malicious oversize-declaration input
// is constructed.
package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func zlibCompress(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// frame prepends the 8-byte big-endian declared uncompressed length to a
// compressed payload.
func frame(declared uint64, compressed []byte) []byte {
	out := make([]byte, 8+len(compressed))
	binary.BigEndian.PutUint64(out[:8], declared)
	copy(out[8:], compressed)
	return out
}

func writeFile(dir, name string, data []byte) {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s (%d bytes): declared=%d\n", path, len(data), binary.BigEndian.Uint64(data[:8]))
}

func main() {
	dir := flag.String("out", "testdata", "output directory for fixtures")
	flag.Parse()
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		panic(err)
	}

	// Benign vector: a small bitmap of 2 bytes (16 one-bit statuses). It
	// decompresses normally and the inflated length equals the declaration.
	smallBitmap := []byte{0b00001011, 0b00000000}
	writeFile(*dir, "valid_small.bin", frame(uint64(len(smallBitmap)), zlibCompress(smallBitmap)))

	// Malicious vector: the compressed payload is a few bytes that inflate to a
	// single byte, yet the declared uncompressed length is 1 TiB. The decoder
	// must reject it solely on the oversized declaration, before inflating.
	tinyPayload := zlibCompress([]byte{0x00})
	writeFile(*dir, "malicious_oversize_declaration.bin", frame(1<<40, tinyPayload))
}
