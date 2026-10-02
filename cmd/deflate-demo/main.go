// Command deflate-demo compresses a synthetic system log with the deflate
// package, shows per-block encoding choices, then decompresses and verifies
// the SHA256 of the restored bytes against the original.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"time"

	"deflate"
)

func generateLogs(n int) []byte {
	levels := []string{"INFO", "WARN", "ERROR", "DEBUG"}
	hosts := []string{"web-01", "web-02", "db-01", "cache-01"}
	msgs := []string{
		"request completed status=200 duration_ms=12 path=/api/v1/users",
		"connection accepted from 10.0.0.14:53322 backlog=128",
		"query executed rows=128 plan=SeqScan table=events",
		"cache hit ratio=0.97 keys=1024 evictions=0",
		"retrying upstream attempt=2 backoff_ms=200 service=billing",
		"disk usage ok mount=/var/log used=61%",
	}
	var b bytes.Buffer
	rng := rand.New(rand.NewSource(42))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for b.Len() < n {
		ts := base.Add(time.Duration(b.Len()) * time.Millisecond).Format("2006-01-02T15:04:05.000Z")
		fmt.Fprintf(&b, "%s %s %s %s\n", ts, levels[rng.Intn(len(levels))],
			hosts[rng.Intn(len(hosts))], msgs[rng.Intn(len(msgs))])
	}
	return b.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func main() {
	size := 4 << 20 // 4 MiB of synthetic system logs
	if len(os.Args) > 1 {
		var mb int
		if _, err := fmt.Sscanf(os.Args[1], "%d", &mb); err == nil && mb > 0 {
			size = mb << 20
		}
	}

	data := generateLogs(size)
	fmt.Printf("input:        %d bytes of synthetic system logs\n", len(data))
	fmt.Printf("sample line:  %s", data[:bytes.IndexByte(data, '\n')+1])

	start := time.Now()
	compressed := deflate.Compress(data)
	encDur := time.Since(start)

	types, err := deflate.BlockTypes(compressed)
	if err != nil {
		fmt.Println("block scan failed:", err)
		os.Exit(1)
	}
	hist := map[int]string{0: "stored", 1: "static", 2: "dynamic"}
	count := map[int]int{}
	for _, ty := range types {
		count[ty]++
	}
	fmt.Printf("compressed:   %d bytes (%.1fx ratio) in %v\n", len(compressed),
		float64(len(data))/float64(len(compressed)), encDur.Round(time.Millisecond))
	fmt.Printf("blocks:       %d total", len(types))
	for _, ty := range []int{0, 1, 2} {
		if count[ty] > 0 {
			fmt.Printf(", %s=%d", hist[ty], count[ty])
		}
	}
	fmt.Println()

	start = time.Now()
	restored, err := deflate.Decompress(compressed)
	if err != nil {
		fmt.Println("decompress failed:", err)
		os.Exit(1)
	}
	decDur := time.Since(start)
	fmt.Printf("decompressed: %d bytes in %v\n", len(restored), decDur.Round(time.Millisecond))

	fmt.Printf("sha256 original:   %s\n", sha256Hex(data))
	fmt.Printf("sha256 restored:   %s\n", sha256Hex(restored))
	if sha256Hex(restored) == sha256Hex(data) && bytes.Equal(restored, data) {
		fmt.Println("round-trip:   OK (byte-identical)")
	} else {
		fmt.Println("round-trip:   FAILED")
		os.Exit(1)
	}
}
