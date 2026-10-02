// Command demo compresses synthetic system logs with the deflate
// package, verifies the round trip via SHA256, and compares against
// the standard library's compress/flate.
package main

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"deflate"
)

func generateLogs(n int) []byte {
	rng := rand.New(rand.NewSource(2026))
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	hosts := []string{"web-01", "web-02", "db-01", "cache-01", "worker-03"}
	svc := []string{"auth", "billing", "search", "upload", "notify"}
	msg := []string{
		"request completed status=200",
		"connection reset by peer",
		"slow query detected duration=812ms",
		"cache hit ratio=0.97",
		"retrying upstream attempt=2",
		"disk usage above threshold usage=91%",
	}
	var b bytes.Buffer
	for b.Len() < n {
		fmt.Fprintf(&b, "2026-10-02T08:%02d:%02dZ %-5s host=%s service=%s pid=%d msg=\"%s\" trace_id=%08x\n",
			rng.Intn(60), rng.Intn(60),
			levels[rng.Intn(len(levels))],
			hosts[rng.Intn(len(hosts))],
			svc[rng.Intn(len(svc))],
			1000+rng.Intn(9000),
			msg[rng.Intn(len(msg))],
			rng.Uint32())
	}
	return b.Bytes()[:n]
}

func main() {
	logs := generateLogs(4 << 20) // 4 MiB of repetitive system logs
	fmt.Printf("input:            %d bytes of synthetic system logs\n", len(logs))

	start := time.Now()
	compressed := deflate.Compress(logs)
	compDur := time.Since(start)

	start = time.Now()
	restored, err := deflate.Decompress(compressed)
	if err != nil {
		panic(err)
	}
	decompDur := time.Since(start)

	sumOrig := sha256.Sum256(logs)
	sumRest := sha256.Sum256(restored)
	fmt.Printf("compressed:       %d bytes (%.2f%% of original) in %v\n",
		len(compressed), 100*float64(len(compressed))/float64(len(logs)), compDur.Round(time.Millisecond))
	fmt.Printf("decompressed:     %d bytes in %v\n", len(restored), decompDur.Round(time.Millisecond))
	fmt.Printf("sha256 original:  %s\n", hex.EncodeToString(sumOrig[:]))
	fmt.Printf("sha256 restored:  %s\n", hex.EncodeToString(sumRest[:]))
	fmt.Printf("round trip exact: %v\n", sumOrig == sumRest)

	// Compare with the standard library at best compression.
	var buf bytes.Buffer
	zw, _ := flate.NewWriter(&buf, flate.BestCompression)
	zw.Write(logs)
	zw.Close()
	fmt.Printf("stdlib flate -9:  %d bytes (%.2f%% of original)\n",
		buf.Len(), 100*float64(buf.Len())/float64(len(logs)))
}
