// Command secded is a small demo CLI for the SECDED interleaved format.
//
//	secded encode -in payload -out payload.scd [-k 11] [-depth 8]
//	secded decode -in payload.scd -out payload.out [-maxbytes N] [-erasures f]
//	secded flip   -in payload.scd -out corrupt.scd -bit N [-bit M ...]
//	secded map    -in payload.scd
//
// encode also writes <out>.map.json (bit-source mapping); decode also
// writes <in>.status.json (per-block report incl. uncorrected blocks).
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"secded"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "encode":
		err = cmdEncode(os.Args[2:])
	case "decode":
		err = cmdDecode(os.Args[2:])
	case "flip":
		err = cmdFlip(os.Args[2:])
	case "map":
		err = cmdMap(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  secded encode -in FILE -out FILE [-k 11] [-depth 8]
  secded decode -in FILE -out FILE [-maxbytes N] [-erasures FILE]
  secded flip   -in FILE -out FILE -bit N [-bit N ...]
  secded map    -in FILE`)
}

func cmdEncode(args []string) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	in, out := fs.String("in", "", "input file"), fs.String("out", "", "output file")
	k := fs.Int("k", 11, "data bits per block")
	depth := fs.Int("depth", 8, "interleave depth")
	if err := fs.Parse(args); err != nil {
		return err
	}
	enc, err := secded.NewEncoder(*k, *depth)
	if err != nil {
		return err
	}
	src, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(*out)
	if err != nil {
		return err
	}
	rep, err := enc.Encode(dst, src)
	if err != nil {
		dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if err := writeJSON(*out+".map.json", rep.SourceMap); err != nil {
		return err
	}
	p := enc.Params
	fmt.Printf("encoded %s -> %s: k=%d r=%d n=%d depth=%d blocks=%d payload=%d bytes (map: %s)\n",
		*in, *out, p.DataBits, p.ParityBits, p.TotalBits, *depth,
		rep.Header.BlockCount, rep.Header.PayloadLen, *out+".map.json")
	return nil
}

func cmdDecode(args []string) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	in, out := fs.String("in", "", "input file"), fs.String("out", "", "output file")
	maxB := fs.Uint64("maxbytes", 1<<30, "decode output bound in bytes")
	erasureFile := fs.String("erasures", "", "file with body-bit erasure indices, one per line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var erasures *secded.BitSet
	if *erasureFile != "" {
		erasures = &secded.BitSet{}
		f, err := os.Open(*erasureFile)
		if err != nil {
			return err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			v, err := strconv.ParseUint(line, 10, 64)
			if err != nil {
				f.Close()
				return fmt.Errorf("bad erasure index %q", line)
			}
			erasures.Set(v)
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return err
		}
	}
	src, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer src.Close()
	payload, _, rep, decErr := secded.Decode(src, erasures, *maxB)
	statusPath := ""
	if len(rep.Blocks) > 0 {
		statusPath = *in + ".status.json"
		if err := writeJSON(statusPath, rep); err != nil {
			return err
		}
	}
	if payload != nil {
		if werr := os.WriteFile(*out, payload, 0o644); werr != nil {
			return werr
		}
	}
	clean, corrected, recovered := 0, 0, 0
	for _, b := range rep.Blocks {
		switch b.Status {
		case "clean":
			clean++
		case "corrected-single":
			corrected++
		case "recovered-erasure":
			recovered++
		}
	}
	fmt.Printf("decoded %s: blocks=%d clean=%d corrected=%d erasure-recovered=%d uncorrectable=%v crc-ok=%v status-file=%q\n",
		*in, len(rep.Blocks), clean, corrected, recovered, rep.Uncorrectable, rep.PayloadCRCOK, statusPath)
	return decErr
}

type bitList []uint64

func (b *bitList) String() string { return fmt.Sprint([]uint64(*b)) }

func (b *bitList) Set(s string) error {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}
	*b = append(*b, v)
	return nil
}

func cmdFlip(args []string) error {
	fs := flag.NewFlagSet("flip", flag.ContinueOnError)
	in, out := fs.String("in", "", "input file"), fs.String("out", "", "output file")
	var bits bitList
	fs.Var(&bits, "bit", "bit index to flip (MSB-first, repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	for _, bit := range bits {
		if bit/8 >= uint64(len(data)) {
			return fmt.Errorf("bit %d out of range (file has %d bits)", bit, len(data)*8)
		}
		data[bit/8] ^= 1 << (7 - bit%8)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("flipped %d bit(s) %v -> %s\n", len(bits), []uint64(bits), *out)
	return nil
}

func cmdMap(args []string) error {
	fs := flag.NewFlagSet("map", flag.ContinueOnError)
	in := fs.String("in", "", "encoded file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	hbuf := make([]byte, secded.HeaderSize)
	if _, err := f.Read(hbuf); err != nil {
		return err
	}
	h, err := secded.UnmarshalHeader(hbuf)
	if err != nil {
		return err
	}
	p, _ := h.Params()
	fmt.Printf("version=%d k=%d r=%d n=%d depth=%d blocks=%d payload=%d bytes crc=%08x\n",
		h.Version, p.DataBits, p.ParityBits, p.TotalBits, h.Depth, h.BlockCount, h.PayloadLen, h.PayloadCRC)
	fmt.Printf("data bit i -> codeword position: %v\n", p.DataPositions())
	offs := make([]uint64, 0, p.TotalBits)
	for pos := 0; pos < p.TotalBits; pos++ {
		off, err := h.BitOffset(0, pos)
		if err != nil {
			return err
		}
		offs = append(offs, off)
	}
	fmt.Printf("block 0 codeword bits at body offsets: %v\n", offs)
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
