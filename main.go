package main

import (
	"flag"
	"fmt"
	"os"
)

const disclaimer = "NOTE: this result covers ONLY the sampled blocks listed above. " +
	"It does NOT prove the integrity of the full archive or of any unsampled block."

func usage() {
	fmt.Fprintf(os.Stderr, `archive-audit: length-weighted, seed-reproducible archive sampling audit

usage:
  archive-audit create    -out FILE [-block-size N] INPUT...
  archive-audit sample    -archive FILE -seed SEED -k N
  archive-audit enumerate -archive FILE [-max-work N]

commands:
  create     build an archive (manifest with per-block logical length and
             SHA-256 summary, plus block payloads) from input files
  sample     draw k blocks without replacement, weighted by logical length,
             using the caller-provided seed; print the full sampling process
             and verify the digests of the sampled blocks only
  enumerate  exhaustively enumerate the random domain of a SMALL archive to
             verify the exact selection probabilities (no modulo bias)
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "create":
		err = cmdCreate(os.Args[2:])
	case "sample":
		err = cmdSample(os.Args[2:])
	case "enumerate":
		err = cmdEnumerate(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	out := fs.String("out", "", "output archive path")
	blockSize := fs.Int64("block-size", 4096, "block size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() == 0 {
		return fmt.Errorf("create requires -out and at least one input file")
	}
	arc, err := CreateArchive(fs.Args(), *blockSize)
	if err != nil {
		return err
	}
	if err := SaveArchive(arc, *out); err != nil {
		return err
	}
	fmt.Printf("created %s: %d blocks, total logical length %s bytes\n",
		*out, len(arc.Blocks), arc.TotalLength())
	return nil
}

func cmdSample(args []string) error {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	path := fs.String("archive", "", "archive path")
	seed := fs.String("seed", "", "caller-provided seed (required, makes sampling reproducible)")
	k := fs.Int("k", 0, "number of blocks to sample")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || *seed == "" || *k <= 0 {
		return fmt.Errorf("sample requires -archive, -seed and -k > 0")
	}
	arc, err := LoadArchive(*path)
	if err != nil {
		return err
	}
	weights := arc.Weights()
	draws, err := Sample(*seed, weights, *k)
	if err != nil {
		return err
	}

	fmt.Printf("archive: %s\n", *path)
	fmt.Printf("seed: %q\n", *seed)
	fmt.Printf("blocks: %d, total logical length: %s\n", len(arc.Blocks), arc.TotalLength())
	zeroWeight := 0
	for _, b := range arc.Blocks {
		if b.Length == 0 {
			zeroWeight++
		}
	}
	if zeroWeight > 0 {
		fmt.Printf("warning: %d zero-length block(s) have weight 0 and can never be selected\n", zeroWeight)
	}
	fmt.Println("sampling process (length-weighted, without replacement):")
	for _, d := range draws {
		b := arc.Blocks[d.BlockIndex]
		fmt.Printf("  draw %d: remaining=%s random=%s -> block %d (source=%s offset=%d length=%d, interval [%s, %s))\n",
			d.Step+1, d.Remaining, d.Random, b.ID, b.Source, b.Offset, b.Length, d.Lo, d.Hi)
	}

	fmt.Println("verification of sampled blocks:")
	allOK := true
	for _, d := range draws {
		b := arc.Blocks[d.BlockIndex]
		ok := VerifyBlock(&b)
		status := "OK"
		if !ok {
			status = "MISMATCH"
			allOK = false
		}
		fmt.Printf("  block %d: sha256 %s (manifest %s)\n", b.ID, status, b.SHA256)
	}
	fmt.Printf("result: %s (%d of %d blocks sampled and checked)\n",
		map[bool]string{true: "SAMPLED BLOCKS VERIFIED", false: "SAMPLE VERIFICATION FAILED"}[allOK],
		*k, len(arc.Blocks))
	fmt.Println(disclaimer)
	if !allOK {
		return fmt.Errorf("sampled block digest mismatch")
	}
	return nil
}

func cmdEnumerate(args []string) error {
	fs := flag.NewFlagSet("enumerate", flag.ContinueOnError)
	path := fs.String("archive", "", "archive path")
	maxWork := fs.Int64("max-work", 50000000, "maximum enumeration steps")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return fmt.Errorf("enumerate requires -archive")
	}
	arc, err := LoadArchive(*path)
	if err != nil {
		return err
	}
	rep, err := Enumerate(arc.Weights(), *maxWork)
	if err != nil {
		return err
	}
	fmt.Printf("archive: %s\n", *path)
	fmt.Printf("blocks: %d, total logical length (random domain size): %d\n", len(arc.Blocks), rep.TotalWeight)
	fmt.Println("first-draw domain check (count of r selecting each block must equal its length):")
	for i, b := range arc.Blocks {
		fmt.Printf("  block %d: count=%d length=%d -> P(first)=%d/%d\n",
			b.ID, rep.FirstCounts[i], b.Length, rep.FirstCounts[i], rep.TotalWeight)
	}
	fmt.Println("second-draw domain check (pairs (i,j) must occur length_i*length_j times, (i,i) never):")
	for i := range arc.Blocks {
		for j := range arc.Blocks {
			fmt.Printf("  (%d -> %d): count=%d\n", arc.Blocks[i].ID, arc.Blocks[j].ID, rep.PairCounts[i][j])
		}
	}
	status := map[bool]string{true: "EXACT", false: "VIOLATION"}[rep.FirstOK && rep.PairsOK]
	fmt.Printf("result: probabilities %s\n", status)
	if !rep.FirstOK || !rep.PairsOK {
		return fmt.Errorf("probability check failed")
	}
	return nil
}
