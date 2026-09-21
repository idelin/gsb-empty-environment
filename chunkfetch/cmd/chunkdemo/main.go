// Command chunkdemo runs an end-to-end demonstration of resumable, verified,
// version-aware chunked downloads against an in-memory fault-injecting source.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"chunkfetch"
	"chunkfetch/simsource"
)

func main() {
	ctx := context.Background()
	var base string
	if len(os.Args) > 1 {
		base = os.Args[1]
	}
	if base == "" {
		base = filepath.Join(os.TempDir(), "chunkdemo")
	}

	data := deterministicData(2000)
	const chunkSize = 300

	// Scenario A: chunk 2 arrives corrupt twice, chunk 4 is truncated once.
	dirA := filepath.Join(base, "A")
	os.RemoveAll(dirA)
	srcA := simsource.New("v1", data, chunkSize)
	srcA.AddFault(2, simsource.FaultCorrupt, 2)
	srcA.AddFault(4, simsource.FaultTruncated, 1)
	fmt.Println("== Scenario A: corrupt chunk retried, truncated chunk retried ==")
	run(ctx, srcA, dirA, data)
	run(ctx, srcA, dirA, data) // second Run: marker short-circuit

	// Scenario B: a chunk stays corrupt forever: the run fails, no marker.
	dirB := filepath.Join(base, "B")
	os.RemoveAll(dirB)
	srcB := simsource.New("v1", data, chunkSize)
	srcB.AddFault(1, simsource.FaultCorrupt, 0) // forever
	fmt.Println("\n== Scenario B: permanently corrupt chunk, then resume ==")
	if _, err := runWithErr(ctx, srcB, dirB, data); err != nil {
		fmt.Println("run failed as expected:", err)
	}
	_, noMarker := os.Stat(filepath.Join(dirB, "COMPLETE"))
	fmt.Printf("COMPLETE marker written? %v (expected false)\n", !os.IsNotExist(noMarker))

	// Fix the source and re-run in the same directory: only chunk 1 is left
	// to fetch, progress resumes from the bitmap.
	srcB2 := simsource.New("v1", data, chunkSize)
	if _, err := runWithErr(ctx, srcB2, dirB, data); err != nil {
		log.Fatal(err)
	}

	// Scenario C: the object changes version: old verified progress is
	// discarded and old-version parts never mix into the new object.
	dirC := filepath.Join(base, "C")
	os.RemoveAll(dirC)
	srcC := simsource.New("v1", data, chunkSize)
	fmt.Println("\n== Scenario C: source version changes; old progress discarded ==")
	if _, err := runWithErr(ctx, srcC, dirC, data); err != nil {
		log.Fatal(err)
	}
	listParts(dirC, "v1")
	newData := deterministicData(1500)
	newData[0] ^= 0xFF
	srcC.Replace("v2", newData, chunkSize)
	if _, err := runWithErr(ctx, srcC, dirC, newData); err != nil {
		log.Fatal(err)
	}
	listParts(dirC, "v2")
}

func run(ctx context.Context, src chunkfetch.Source, dir string, data []byte) *chunkfetch.Result {
	res, err := runWithErr(ctx, src, dir, data)
	if err != nil {
		log.Fatal(err)
	}
	return res
}

func runWithErr(ctx context.Context, src chunkfetch.Source, dir string, data []byte) (*chunkfetch.Result, error) {
	f := chunkfetch.New(chunkfetch.Config{
		Dir:            dir,
		Attempts:       4,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		Logger:         func(m string) { fmt.Println("   ", m) },
	})
	res, err := f.Run(ctx, src)
	if err != nil {
		return res, err
	}
	out, rerr := os.ReadFile(filepath.Join(dir, "object.bin"))
	if rerr != nil {
		return res, rerr
	}
	sum := sha256.Sum256(out)
	fmt.Printf("version=%s chunks=%d outputBytes=%d digestOk=%v\n",
		res.Version, res.Chunks, len(out), string(out) == string(data) && compare(sum[:], mustWhole(data)))
	return res, nil
}

func compare(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustWhole(data []byte) []byte {
	s := sha256.Sum256(data)
	return s[:]
}

func listParts(dir, version string) {
	entries, err := os.ReadDir(filepath.Join(dir, "chunks"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("part directories after %s: %v\n", version, names(entries))
}

func names(es []os.DirEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func deterministicData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*73 + 11) % 251)
	}
	return b
}
