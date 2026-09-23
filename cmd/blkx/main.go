package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"blkx"
)

func usage() {
	fmt.Fprintf(os.Stderr, `blkx - streaming block-encoding transcoder (format A v1 -> format B v2)

usage:
  blkx encode     -in FILE -out FILE.bka [-meta STRING] [-block BYTES]
  blkx transcode  -in FILE.bka -out FILE.bkb
  blkx decode     -in FILE.bkb -out FILE [-quota BYTES]
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "encode":
		encodeCmd(os.Args[2:])
	case "transcode":
		transcodeCmd(os.Args[2:])
	case "decode":
		decodeCmd(os.Args[2:])
	default:
		usage()
	}
}

func encodeCmd(args []string) {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output .bka file")
	meta := fs.String("meta", "{}", "metadata/summary to embed")
	block := fs.Int("block", 65536, "block size in bytes")
	fs.Parse(args)
	if *in == "" || *out == "" || *block <= 0 || *block > blkx.MaxBlockSize {
		usage()
	}
	src, err := os.Open(*in)
	check(err)
	defer src.Close()
	st, err := src.Stat()
	check(err)

	dst, err := os.Create(*out)
	check(err)
	aw, err := blkx.NewAWriter(dst, []byte(*meta), uint64(st.Size()))
	check(err)
	buf := make([]byte, *block)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			check(aw.WriteBlock(buf[:n]))
		}
		if rerr == io.EOF {
			break
		}
		check(rerr)
	}
	check(aw.Finish())
	check(dst.Close())
	fmt.Printf("encoded %s -> %s (%d bytes declared)\n", *in, *out, st.Size())
}

func transcodeCmd(args []string) {
	fs := flag.NewFlagSet("transcode", flag.ExitOnError)
	in := fs.String("in", "", "input .bka file")
	out := fs.String("out", "", "output .bkb file")
	fs.Parse(args)
	if *in == "" || *out == "" {
		usage()
	}
	src, err := os.Open(*in)
	check(err)
	defer src.Close()
	check(blkx.TranscodeFile(*out, src))
	fmt.Printf("transcoded %s -> %s\n", *in, *out)
}

func decodeCmd(args []string) {
	fs := flag.NewFlagSet("decode", flag.ExitOnError)
	in := fs.String("in", "", "input .bkb file")
	out := fs.String("out", "", "output file")
	quota := fs.Uint64("quota", 1<<30, "max decoded bytes")
	fs.Parse(args)
	if *in == "" || *out == "" {
		usage()
	}
	src, err := os.Open(*in)
	check(err)
	defer src.Close()

	tmp := *out + ".tmp"
	dst, err := os.Create(tmp)
	check(err)
	meta, n, derr := blkx.Decode(dst, src, *quota)
	if derr != nil {
		dst.Close()
		os.Remove(tmp)
		check(derr)
	}
	check(dst.Close())
	check(os.Rename(tmp, *out))
	fmt.Printf("decoded %s -> %s (%d bytes, meta %s)\n", *in, *out, n, meta)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "blkx:", err)
		os.Exit(1)
	}
}
