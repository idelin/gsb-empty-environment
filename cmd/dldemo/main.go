// dldemo simulates a parallel ranged download against a local in-memory
// server, with optional fault injection from a JSON fault file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"paradl"
)

func main() {
	var (
		size    = flag.Int64("size", 1<<20, "entity size in bytes")
		seed    = flag.Int64("seed", 42, "content seed")
		chunk   = flag.Int64("chunk", 1<<16, "chunk size in bytes")
		workers = flag.Int("workers", 4, "parallel fetch workers")
		faults  = flag.String("faults", "", "path to fault file (JSON)")
		dst     = flag.String("dst", "download.bin", "destination file")
		ckpt    = flag.String("ckpt", "download.ckpt.json", "checkpoint file")
	)
	flag.Parse()

	ff, err := paradl.LoadFaults(*faults)
	if err != nil {
		log.Fatal(err)
	}
	src := paradl.NewLocalSource(*seed, *size, "v0", ff)

	d := paradl.NewDownloader(src, *dst, *ckpt, ff)
	d.ChunkSize = *chunk
	d.Workers = *workers

	err = d.Run(context.Background())
	if err == paradl.ErrSimulatedCrash {
		fmt.Printf("CRASH at checkpoint cut point after %d requests; rerun to resume\n", src.Requests())
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("download failed: %v", err)
	}

	got, _ := os.ReadFile(*dst)
	want := src.Content()
	if paradl.ChecksumBytes(got) != paradl.ChecksumBytes(want) {
		log.Fatal("final checksum mismatch")
	}
	fmt.Printf("OK: %d bytes, version %s, %d requests, checksum %s\n",
		len(got), src.Version(), src.Requests(), paradl.ChecksumBytes(got)[:16])
}
