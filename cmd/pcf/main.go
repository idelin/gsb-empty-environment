// Command pcf encodes, decodes and deliberately corrupts PCF frame streams.
//
//	pcf encode  [-frame N]                          < raw      > stream
//	pcf decode  [-budget N] [-chunk N]              < stream   > raw      (report on stderr)
//	pcf corrupt -mode M -offset N [-bit K] [-len L] < stream   > stream
//
// decode exits 0 when the stream is clean and 1 when any bad range or gap
// was reported.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"pcf"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "encode":
		cmdEncode(os.Args[2:])
	case "decode":
		cmdDecode(os.Args[2:])
	case "corrupt":
		cmdCorrupt(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  pcf encode  [-frame N]                            split stdin into frames
  pcf decode  [-budget N] [-chunk N]                decode frames to stdout, report to stderr
  pcf corrupt -mode M -offset N [-bit K] [-len L]   corrupt a stream
      modes: flip | fakemagic | truncate | trunchead | badtable`)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "pcf:", err)
		os.Exit(2)
	}
}

func cmdEncode(args []string) {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	size := fs.Int("frame", 65536, "raw bytes per frame")
	fs.Parse(args)
	data, err := ioutil.ReadAll(os.Stdin)
	check(err)
	var seq uint32
	for len(data) > 0 {
		n := *size
		if n > len(data) {
			n = len(data)
		}
		if _, err := os.Stdout.Write(pcf.EncodeFrame(seq, data[:n])); err != nil {
			check(err)
		}
		data = data[n:]
		seq++
	}
}

func cmdDecode(args []string) {
	fs := flag.NewFlagSet("decode", flag.ExitOnError)
	budget := fs.Int("budget", 0, "resync scan steps per read (0 = unlimited)")
	chunk := fs.Int("chunk", 1<<16, "read chunk size in bytes")
	fs.Parse(args)

	r := pcf.NewReader(pcf.Options{ScanBudget: *budget})
	problems := 0
	emit := func(evs []pcf.Event) {
		for _, e := range evs {
			switch e.Kind {
			case pcf.EventFrame:
				if _, err := os.Stdout.Write(e.Data); err != nil {
					check(err)
				}
				fmt.Fprintf(os.Stderr, "frame seq=%d offset=%d len=%d raw=%d\n",
					e.Seq, e.Offset, e.Length, len(e.Data))
			case pcf.EventBad:
				problems++
				fmt.Fprintf(os.Stderr, "bad   offset=%d len=%d reason=%s\n",
					e.Offset, e.Length, e.Reason)
			case pcf.EventGap:
				problems++
				fmt.Fprintf(os.Stderr, "gap   offset=%d len=%d (undetermined)\n",
					e.Offset, e.Length)
			}
		}
	}

	buf := make([]byte, *chunk)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			emit(r.Feed(buf[:n]))
			// Drain work paused by the scan budget; stop when the reader
			// is blocked waiting for more input.
			for r.Buffered() > 0 {
				before := r.Buffered()
				emit(r.Feed(nil))
				if r.Buffered() == before {
					break
				}
			}
		}
		if err == io.EOF {
			break
		}
		check(err)
	}
	emit(r.Close())
	if problems > 0 {
		os.Exit(1)
	}
}

func cmdCorrupt(args []string) {
	fs := flag.NewFlagSet("corrupt", flag.ExitOnError)
	mode := fs.String("mode", "flip", "flip | fakemagic | truncate | trunchead | badtable")
	off := fs.Int("offset", 0, "byte offset (badtable: start of the target frame)")
	bit := fs.Uint("bit", 0, "bit to flip, 0 = LSB (flip mode)")
	n := fs.Int("len", 64, "length parameter: delete length (truncate), filler length (fakemagic), head length (trunchead)")
	fs.Parse(args)

	data, err := ioutil.ReadAll(os.Stdin)
	check(err)
	var out []byte
	switch *mode {
	case "flip":
		out = pcf.FlipBit(data, *off, *bit)
	case "fakemagic":
		out = pcf.InsertFakeMagic(data, *off, bytes.Repeat([]byte{0xA5}, *n)...)
	case "truncate":
		if *off+*n > len(data) {
			*n = len(data) - *off
		}
		out = append(data[:*off:*off], data[*off+*n:]...)
	case "trunchead":
		out = pcf.InsertTruncatedHead(data, *off, 0xFFFF, []byte("x"), *n)
	case "badtable":
		out = pcf.CorruptCodeTable(data, *off)
	default:
		fmt.Fprintln(os.Stderr, "pcf: unknown corrupt mode", *mode)
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		check(err)
	}
}
