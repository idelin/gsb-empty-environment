// Command dictcomp trains a fragment dictionary from sample files and
// encodes/decodes files with it.
//
//	dictcomp train  -train <path>... [-val <path>...] [-dict out] [-min N] [-max N] [-mode auto|exact|greedy]
//	dictcomp encode -dict <file> -in <file> -out <file>
//	dictcomp decode -dict <file> -in <file> -out <file> [-maxout N]
//
// Paths may be files or directories (directories are walked in sorted
// order). Training and validation samples are always kept separate.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dictcomp/internal/comp"
	"dictcomp/internal/wire"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "train":
		err = cmdTrain(os.Args[2:])
	case "encode":
		err = cmdEncode(os.Args[2:])
	case "decode":
		err = cmdDecode(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "dictcomp:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  dictcomp train  -train <path>... [-val <path>...] [-dict out] [-min N] [-max N] [-mode auto|exact|greedy]
  dictcomp encode -dict <file> -in <file> -out <file>
  dictcomp decode -in <file> -out <file> [-maxout N]`)
}

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func cmdTrain(args []string) error {
	fs := flag.NewFlagSet("train", flag.ContinueOnError)
	var trainPaths, valPaths multiFlag
	fs.Var(&trainPaths, "train", "training sample file or directory (repeatable)")
	fs.Var(&valPaths, "val", "validation sample file or directory (repeatable)")
	dictOut := fs.String("dict", "dict.dzd", "dictionary output file")
	minLen := fs.Int("min", 4, "minimum fragment length")
	maxLen := fs.Int("max", 32, "maximum fragment length")
	mode := fs.String("mode", "auto", "selection mode: auto|exact|greedy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(trainPaths) == 0 {
		return fmt.Errorf("at least one -train path is required")
	}
	var m comp.Mode
	switch *mode {
	case "auto":
		m = comp.ModeAuto
	case "exact":
		m = comp.ModeExact
	case "greedy":
		m = comp.ModeGreedy
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}

	trainSamples, err := readSamples(trainPaths)
	if err != nil {
		return err
	}
	valSamples, err := readSamples(valPaths)
	if err != nil {
		return err
	}

	sel, err := comp.SelectDictionary(trainSamples, *minLen, *maxLen, m)
	if err != nil {
		return err
	}

	dictBytes, err := wire.WriteDict(sel.Dict)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*dictOut, dictBytes, 0o644); err != nil {
		return err
	}

	modeDesc := "greedy (heuristic: feasible result, not guaranteed optimal)"
	if sel.Exact {
		modeDesc = "exact (exhaustive: provably optimal dictionary and encoding)"
	}
	fmt.Printf("mode:            %s\n", modeDesc)
	fmt.Printf("candidates:      %d repeated fragments (%d maximal after dominance filtering)\n", sel.Candidates, sel.Maximal)
	fmt.Printf("dictionary:      %d entries, %d bytes storage (counted in every total below)\n",
		len(sel.Dict), comp.DictStoreCost(sel.Dict))
	if len(sel.Dict) == 0 {
		fmt.Printf("note:            no fragment repaid its storage; dictionary is empty, data is stored as literals\n")
	}
	printSetReport("train", trainSamples, sel.Dict, sel.TrainTotal)
	if len(valSamples) > 0 {
		var valTotal int64
		for _, s := range valSamples {
			_, c := comp.OptimalParse(s, sel.Dict)
			valTotal += c
		}
		valTotal += wire.FileOverhead(sel.Dict)
		printSetReport("validation", valSamples, sel.Dict, valTotal)
	}
	achieved := sel.EmptyTotal - sel.TrainTotal
	if sel.Exact {
		fmt.Printf("bound:           train total is provably optimal (%d bytes)\n", sel.TrainTotal)
	} else {
		pct := 0.0
		if sel.GainBound > 0 {
			pct = float64(achieved) / float64(sel.GainBound) * 100
		}
		fmt.Printf("gain bound:      any dictionary gains at most %d bytes on train; achieved %d (%.1f%% of bound)\n",
			sel.GainBound, achieved, pct)
		fmt.Printf("size floor:      no dictionary compresses train below %d bytes; achieved %d\n",
			sel.OptimalFloor(), sel.TrainTotal)
	}
	fmt.Printf("dictionary file: %s\n", *dictOut)
	return nil
}

func printSetReport(name string, samples [][]byte, dict [][]byte, total int64) {
	var orig int64
	for _, s := range samples {
		orig += int64(len(s))
	}
	ratio := 1.0
	if orig > 0 {
		ratio = float64(total) / float64(orig)
	}
	fmt.Printf("%-16s %d samples, %d bytes -> %d bytes (ratio %.4f, dictionary included)\n",
		name+":", len(samples), orig, total, ratio)
}

func cmdEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	dictPath := fs.String("dict", "dict.dzd", "dictionary file")
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return fmt.Errorf("-in and -out are required")
	}
	dict, err := readDict(*dictPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	tokens, _ := comp.OptimalParse(data, dict)
	enc, err := wire.Encode(dict, tokens, data)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, enc, 0o644); err != nil {
		return err
	}
	ratio := 1.0
	if len(data) > 0 {
		ratio = float64(len(enc)) / float64(len(data))
	}
	fmt.Printf("encode: %d -> %d bytes (ratio %.4f, dictionary embedded in file)\n",
		len(data), len(enc), ratio)
	return nil
}

func cmdDecode(args []string) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file")
	maxOut := fs.Int64("maxout", 1<<30, "maximum decompressed size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return fmt.Errorf("-in and -out are required")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	_, plain, err := wire.Decode(data, *maxOut)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, plain, 0o644); err != nil {
		return err
	}
	fmt.Printf("decode: %d -> %d bytes (length and CRC-32 verified)\n", len(data), len(plain))
	return nil
}

func readDict(path string) ([][]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return wire.ReadDict(data)
}

// readSamples expands files and directories into a deterministically
// ordered slice of samples.
func readSamples(paths []string) ([][]byte, error) {
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, p)
			continue
		}
		err = filepath.Walk(p, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	var samples [][]byte
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		samples = append(samples, data)
	}
	return samples, nil
}
