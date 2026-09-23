// Command dictcomp builds a fixed dictionary of repeated substrings from a
// finite training set (within a byte budget that includes dictionary
// overhead) and compresses streams against it. Standard library only.
//
// Subcommands:
//
//	train   select a dictionary from training samples and report ratios
//	encode  compress one file with a dictionary
//	decode  decompress one file (output size bounded by -maxout)
//	eval    report compression of files with an existing dictionary
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case "train":
		err = cmdTrain(args[1:], stdout)
	case "encode":
		err = cmdEncode(args[1:], stdout)
	case "decode":
		err = cmdDecode(args[1:], stdout)
	case "eval":
		err = cmdEval(args[1:], stdout)
	default:
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "dictcomp: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  dictcomp train [-mode exact|heuristic] [-budget N] [-minlen N] [-maxlen N]
                 [-maxexact N] [-o dictfile] [-valid file]... trainfile...
  dictcomp encode -dict dictfile [-exact] [-o out] input
  dictcomp decode -dict dictfile [-maxout N] [-o out] input
  dictcomp eval -dict dictfile file...
`)
}

// stringList collects repeated flags.
type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func readFiles(paths []string) ([][]byte, error) {
	samples := make([][]byte, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		samples = append(samples, b)
	}
	return samples, nil
}

func totalLen(samples [][]byte) int {
	n := 0
	for _, s := range samples {
		n += len(s)
	}
	return n
}

// encodeSamples encodes every sample and returns the real on-disk stream
// sizes. Exact mode uses the exact DP parser; heuristic mode the fast one.
func encodeSamples(samples [][]byte, dict [][]byte, dictCRC uint32, exact bool) (streams [][]byte, total int) {
	di := buildDictIndex(dict)
	for _, s := range samples {
		var toks []token
		if exact {
			toks = exactParse(s, di)
		} else {
			toks = fastParse(s, di)
		}
		st := encodeStream(dictCRC, s, toks)
		streams = append(streams, st)
		total += len(st)
	}
	return streams, total
}

func pct(part, whole int) float64 {
	if whole == 0 {
		return 0
	}
	return 100 * float64(part) / float64(whole)
}

// reportSet prints the compression ratio of one sample set, dictionary
// overhead included, and returns whether the dictionary pays off.
func reportSet(w io.Writer, name string, samples [][]byte, dict [][]byte, dictBytes int, dictCRC uint32, exact bool) (int, int) {
	orig := totalLen(samples)
	_, enc := encodeSamples(samples, dict, dictCRC, exact)
	withDict := enc + dictBytes
	fmt.Fprintf(w, "%s: %d samples, %d bytes -> %d bytes encoded + %d dict = %d bytes (%.1f%% of original)\n",
		name, len(samples), orig, enc, dictBytes, withDict, pct(withDict, orig))
	return orig, withDict
}

func cmdTrain(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("train", flag.ContinueOnError)
	fs.SetOutput(w)
	mode := fs.String("mode", "heuristic", "selection mode: exact or heuristic")
	budget := fs.Int("budget", 4096, "byte budget for the serialized dictionary")
	minLen := fs.Int("minlen", 3, "minimum phrase length")
	maxLen := fs.Int("maxlen", 32, "maximum phrase length")
	maxExact := fs.Int("maxexact", 20, "maximum candidate count for exact mode")
	out := fs.String("o", "", "write dictionary to this file")
	var validPaths stringList
	fs.Var(&validPaths, "valid", "validation sample file (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("train: no training samples given")
	}
	train, err := readFiles(fs.Args())
	if err != nil {
		return err
	}
	valid, err := readFiles(validPaths)
	if err != nil {
		return err
	}
	// Deterministic mode: sample order must not influence the result.
	sortSamplesCanonical(train)
	sortSamplesCanonical(valid)

	cands, truncated := generateCandidates(train, *minLen, *maxLen)
	fmt.Fprintf(w, "candidates: %d repeated substrings (length %d..%d)", len(cands), *minLen, *maxLen)
	if truncated {
		fmt.Fprint(w, ", candidate generation truncated at cap")
	}
	fmt.Fprintln(w)

	var sel selection
	switch *mode {
	case "exact":
		sel, err = selectExact(train, cands, *budget, *maxExact)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "mode: exact (optimal dictionary and encoding over %d candidates, budget %d)\n", len(cands), *budget)
	case "heuristic":
		sel = selectHeuristic(train, cands, *budget)
		fmt.Fprintf(w, "mode: heuristic (feasible result, no optimality guarantee, budget %d)\n", *budget)
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}
	exact := *mode == "exact"
	fmt.Fprintf(w, "dictionary: %d entries, %d bytes serialized\n", len(sel.dict), sel.dictBytes)

	dictFile := marshalDict(sel.dict)
	dictCRC := dictFileCRC(dictFile)
	trainOrig, trainTotal := reportSet(w, "train", train, sel.dict, sel.dictBytes, dictCRC, exact)
	if len(valid) > 0 {
		reportSet(w, "valid", valid, sel.dict, sel.dictBytes, dictCRC, exact)
	}

	savings := trainOrig - trainTotal
	if !exact {
		ub := savingsUpperBound(train, cands, *budget)
		fmt.Fprintf(w, "heuristic savings on train: %d bytes; upper bound on any dictionary's savings: <= %d bytes (within candidate model)\n", savings, ub)
	}
	if len(sel.dict) == 0 {
		fmt.Fprintln(w, "note: no dictionary fits the budget with positive gain; storing samples uncompressed is optimal under this model")
	} else if savings <= 0 {
		fmt.Fprintln(w, "note: total overhead (dictionary + stream headers) exceeds encoding savings on the train set; compression is not worthwhile for this data")
	}

	if *out != "" {
		if err := os.WriteFile(*out, dictFile, 0644); err != nil {
			return err
		}
		fmt.Fprintf(w, "wrote dictionary to %s\n", *out)
	}
	return nil
}

func loadDict(path string) ([][]byte, []byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	d, err := unmarshalDict(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return d, b, nil
}

func cmdEncode(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	fs.SetOutput(w)
	dictPath := fs.String("dict", "", "dictionary file")
	exact := fs.Bool("exact", false, "use exact DP parser (small inputs only)")
	out := fs.String("o", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dictPath == "" || fs.NArg() != 1 {
		return fmt.Errorf("encode: -dict and exactly one input file are required")
	}
	dict, dictFile, err := loadDict(*dictPath)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	di := buildDictIndex(dict)
	var toks []token
	if *exact {
		toks = exactParse(src, di)
	} else {
		toks = fastParse(src, di)
	}
	stream := encodeStream(dictFileCRC(dictFile), src, toks)
	if *out == "" {
		_, err = w.Write(stream)
		return err
	}
	fmt.Fprintf(w, "%s: %d -> %d bytes (%.1f%%)\n", fs.Arg(0), len(src), len(stream), pct(len(stream), len(src)))
	return os.WriteFile(*out, stream, 0644)
}

func cmdDecode(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	fs.SetOutput(w)
	dictPath := fs.String("dict", "", "dictionary file")
	maxOut := fs.Int("maxout", 1<<28, "maximum allowed decompressed size in bytes")
	out := fs.String("o", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dictPath == "" || fs.NArg() != 1 {
		return fmt.Errorf("decode: -dict and exactly one input file are required")
	}
	dict, _, err := loadDict(*dictPath)
	if err != nil {
		return err
	}
	stream, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	dec, err := decodeStream(stream, dict, *maxOut)
	if err != nil {
		return err
	}
	if *out == "" {
		_, err = w.Write(dec)
		return err
	}
	fmt.Fprintf(w, "%s: %d -> %d bytes\n", fs.Arg(0), len(stream), len(dec))
	return os.WriteFile(*out, dec, 0644)
}

func cmdEval(args []string, w io.Writer) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(w)
	dictPath := fs.String("dict", "", "dictionary file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dictPath == "" || fs.NArg() == 0 {
		return fmt.Errorf("eval: -dict and at least one file are required")
	}
	dict, dictFile, err := loadDict(*dictPath)
	if err != nil {
		return err
	}
	samples, err := readFiles(fs.Args())
	if err != nil {
		return err
	}
	sortSamplesCanonical(samples)
	dictBytes := len(dictFile)
	orig, withDict := reportSet(w, "eval", samples, dict, dictBytes, dictFileCRC(dictFile), false)
	if withDict > orig {
		fmt.Fprintln(w, "note: including dictionary overhead, compression is not worthwhile for this set")
	}
	return nil
}
