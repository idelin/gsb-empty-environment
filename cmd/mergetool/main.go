// mergetool is a resumable multi-way file merge tool.
//
//	mergetool gen    -dir D -sources N -records M        generate sorted runs
//	mergetool merge  -o OUT -ckpt CK [-mem 16MB] IN...   merge (auto-resume)
//	mergetool verify -o OUT [-ckpt CK] IN...             full-sort check
package main

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"mergetool/merge"
)

func parseSize(s string) (int64, error) {
	mult := int64(1)
	for _, su := range []struct {
		suffix string
		mult   int64
	}{{"KB", 1 << 10}, {"MB", 1 << 20}, {"GB", 1 << 30}, {"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}} {
		if strings.HasSuffix(s, su.suffix) {
			mult = su.mult
			s = strings.TrimSuffix(s, su.suffix)
			break
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return v * mult, nil
}

func cmdGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	dir := fs.String("dir", ".", "output directory")
	sources := fs.Int("sources", 4, "number of input runs")
	records := fs.Int("records", 1000, "records per run")
	maxKey := fs.Int("maxkey", 10000, "key space size")
	maxPayload := fs.Int("maxpayload", 64, "max payload bytes")
	seed := fs.Int64("seed", time.Now().UnixNano(), "random seed")
	fs.Parse(args)
	paths, err := merge.GenerateInputs(*dir, *sources, *records, *maxKey, *maxPayload, rand.New(rand.NewSource(*seed)))
	if err != nil {
		return err
	}
	fmt.Printf("generated %d runs in %s\n", len(paths), *dir)
	return nil
}

func cmdMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	out := fs.String("o", "out.run", "output file")
	ckpt := fs.String("ckpt", "out.ckpt", "checkpoint file")
	mem := fs.String("mem", "16MB", "memory limit on buffered bytes")
	block := fs.String("block", "64KB", "I/O block size")
	ckptEvery := fs.Int("ckptevery", 1024, "records between checkpoints")
	fs.Parse(args)
	inputs := fs.Args()
	if len(inputs) == 0 {
		return fmt.Errorf("no input files")
	}
	memLimit, err := parseSize(*mem)
	if err != nil {
		return err
	}
	blockSize, err := parseSize(*block)
	if err != nil {
		return err
	}
	m, err := merge.NewMerger(merge.Options{
		Inputs:          inputs,
		OutputPath:      *out,
		CheckpointPath:  *ckpt,
		MemLimit:        memLimit,
		BlockSize:       int(blockSize),
		CheckpointEvery: *ckptEvery,
		Resume:          true,
	})
	if err != nil {
		return err
	}
	defer m.Close()
	err = m.Run()
	if errors.Is(err, merge.ErrDone) {
		fmt.Println("merge already complete (checkpoint says done); nothing to do")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("merge complete: %s (peak buffered bytes: %d)\n", *out, m.PeakMem)
	return nil
}

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	out := fs.String("o", "out.run", "output file")
	ckpt := fs.String("ckpt", "", "checkpoint file (optional)")
	fs.Parse(args)
	inputs := fs.Args()
	if len(inputs) == 0 {
		return fmt.Errorf("no input files")
	}
	var ck *merge.Checkpoint
	if *ckpt != "" {
		var err error
		ck, err = merge.LoadCheckpoint(*ckpt)
		if err != nil {
			return err
		}
	}
	if err := merge.Verify(*out, inputs, ck); err != nil {
		return err
	}
	fmt.Println("verify OK: output matches full sort of inputs")
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mergetool <gen|merge|verify> [flags] [inputs...]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = cmdGen(os.Args[2:])
	case "merge":
		err = cmdMerge(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
