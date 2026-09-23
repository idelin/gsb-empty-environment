// Command blkconv packs, transcodes and unpacks block-encoded streams.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"blkconv"
)

type metaFlag map[string]string

func (m *metaFlag) String() string { return fmt.Sprint(map[string]string(*m)) }

func (m *metaFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return fmt.Errorf("want key=value, got %q", s)
	}
	if *m == nil {
		*m = map[string]string{}
	}
	(*m)[k] = v
	return nil
}

func parseSize(s string) (uint64, error) {
	mult := uint64(1)
	rest := s
	if len(s) > 0 {
		switch s[len(s)-1] {
		case 'K', 'k':
			mult, rest = 1<<10, s[:len(s)-1]
		case 'M', 'm':
			mult, rest = 1<<20, s[:len(s)-1]
		case 'G', 'g':
			mult, rest = 1<<30, s[:len(s)-1]
		}
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}
	return n * mult, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  blkconv pack   -fmt a|b -in FILE -out FILE [-meta k=v ...]
  blkconv xcode  -in FILE -out FILE [-tfmt a|b] [-max N]
  blkconv unpack -in FILE -out FILE [-max N]
  blkconv info   -in FILE

xcode transcodes between block encodings streaming, preserving metadata,
original length and SHA-256 digest. Output appears only on full success;
bad input never gets a commit marker. -max bounds decoded output (suffixes
K/M/G allowed, default 1G).`)
	os.Exit(2)
}

func need(name, v string) {
	if v == "" {
		fmt.Fprintf(os.Stderr, "missing -%s\n", name)
		os.Exit(2)
	}
}

func printHeader(prefix string, h *blkconv.Header) {
	fmt.Printf("%s format=%s version=%d origLen=%d sha256=%s\n",
		prefix, h.Format, blkconv.Version, h.OrigLen, hex.EncodeToString(h.Digest[:]))
	if len(h.Meta) > 0 {
		keys := make([]string, 0, len(h.Meta))
		for k := range h.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  meta %s=%s\n", k, h.Meta[k])
		}
	}
}

func cmdPack(args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	fmts := fs.String("fmt", "a", "block encoding a|b")
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file")
	var meta metaFlag
	fs.Var(&meta, "meta", "metadata key=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	need("in", *in)
	need("out", *out)
	f, err := blkconv.ParseFormat(*fmts)
	if err != nil {
		return err
	}
	h, err := blkconv.PackFile(*in, *out, f, meta)
	if err != nil {
		return err
	}
	printHeader("packed:", h)
	return nil
}

func cmdXcode(args []string) error {
	fs := flag.NewFlagSet("xcode", flag.ContinueOnError)
	tfmts := fs.String("tfmt", "", "target encoding a|b (default: the other one)")
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file")
	maxs := fs.String("max", "", "decode budget (default 1G)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	need("in", *in)
	need("out", *out)
	max := uint64(0)
	if *maxs != "" {
		var err error
		if max, err = parseSize(*maxs); err != nil {
			return err
		}
	}
	src, err := blkconv.ReadHeaderFile(*in)
	if err != nil {
		return err
	}
	tfmt := blkconv.FormatB
	if src.Format == blkconv.FormatB {
		tfmt = blkconv.FormatA
	}
	if *tfmts != "" {
		if tfmt, err = blkconv.ParseFormat(*tfmts); err != nil {
			return err
		}
	}
	if _, err := blkconv.TranscodeFile(*in, *out, tfmt, max); err != nil {
		return err
	}
	h, err := blkconv.ReadHeaderFile(*out)
	if err != nil {
		return err
	}
	printHeader(fmt.Sprintf("transcoded %s->%s:", src.Format, tfmt), h)
	return nil
}

func cmdUnpack(args []string) error {
	fs := flag.NewFlagSet("unpack", flag.ContinueOnError)
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file")
	maxs := fs.String("max", "", "decode budget (default 1G)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	need("in", *in)
	need("out", *out)
	max := uint64(0)
	if *maxs != "" {
		var err error
		if max, err = parseSize(*maxs); err != nil {
			return err
		}
	}
	h, err := blkconv.UnpackFile(*in, *out, max)
	if err != nil {
		return err
	}
	printHeader("unpacked:", h)
	return nil
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	in := fs.String("in", "", "input file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	need("in", *in)
	h, err := blkconv.ReadHeaderFile(*in)
	if err != nil {
		return err
	}
	printHeader("header:", h)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "pack":
		err = cmdPack(os.Args[2:])
	case "xcode":
		err = cmdXcode(os.Args[2:])
	case "unpack":
		err = cmdUnpack(os.Args[2:])
	case "info":
		err = cmdInfo(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
