// Command framearc encodes, decodes, and deliberately corrupts
// framearc archives.
//
//	framearc encode [-size N]   < plain > archive
//	framearc decode             < archive > plain   (report on stderr)
//	framearc corrupt -mode M [-off N] [-bit K] [-n K] [-frame I] < archive > damaged
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"framearc"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: framearc <encode|decode|corrupt> [flags]")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
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
	}
}

func cmdEncode(args []string) {
	fs := flag.NewFlagSet("encode", flag.ExitOnError)
	size := fs.Int("size", 4096, "max bytes per frame")
	fs.Parse(args)
	data, err := ioutil.ReadAll(os.Stdin)
	check(err)
	var arc []byte
	seq := uint32(0)
	for len(data) > 0 {
		n := *size
		if n > len(data) {
			n = len(data)
		}
		arc, err = framearc.AppendFrame(arc, seq, data[:n])
		check(err)
		data = data[n:]
		seq++
	}
	_, err = os.Stdout.Write(arc)
	check(err)
}

func cmdDecode(args []string) {
	fs := flag.NewFlagSet("decode", flag.ExitOnError)
	budget := fs.Int("budget", 0, "max resync candidates per call (0 = unlimited)")
	fs.Parse(args)
	arc, err := ioutil.ReadAll(os.Stdin)
	check(err)
	r := &framearc.Reader{Budget: *budget}
	var events []framearc.Event
	const chunk = 64 * 1024
	for i := 0; i < len(arc); i += chunk {
		j := i + chunk
		if j > len(arc) {
			j = len(arc)
		}
		events = append(events, r.Feed(arc[i:j])...)
		for r.Paused() {
			events = append(events, r.Pump()...)
		}
	}
	events = append(events, r.Close()...)
	for r.Paused() {
		events = append(events, r.Pump()...)
	}
	damaged := false
	for _, ev := range events {
		switch ev.Kind {
		case framearc.EventFrame:
			fmt.Fprintf(os.Stderr, "frame    seq=%d range=[%d,%d) decoded=%d\n",
				ev.Seq, ev.Start, ev.End, len(ev.Data))
			_, err = os.Stdout.Write(ev.Data)
			check(err)
		case framearc.EventBadFrame:
			damaged = true
			fmt.Fprintf(os.Stderr, "badframe seq=%d range=[%d,%d) reason=%q\n",
				ev.Seq, ev.Start, ev.End, ev.Reason)
		case framearc.EventGap:
			damaged = true
			fmt.Fprintf(os.Stderr, "gap      range=[%d,%d)\n", ev.Start, ev.End)
		}
	}
	if damaged {
		os.Exit(3)
	}
}

func cmdCorrupt(args []string) {
	fs := flag.NewFlagSet("corrupt", flag.ExitOnError)
	mode := fs.String("mode", "", "insert-magic|drop|flip|wrong-table")
	off := fs.Int("off", -1, "byte offset (default: middle of input)")
	bit := fs.Uint("bit", 0, "bit to flip (flip mode)")
	n := fs.Int("n", 8, "bytes to drop (drop mode)")
	frame := fs.Int("frame", 0, "frame index (wrong-table mode)")
	fs.Parse(args)
	arc, err := ioutil.ReadAll(os.Stdin)
	check(err)
	if *off < 0 {
		*off = len(arc) / 2
	}
	var out []byte
	switch *mode {
	case "insert-magic":
		out = framearc.InsertBytes(arc, *off, framearc.FakeMagic())
	case "drop":
		out = framearc.DropBytes(arc, *off, *n)
	case "flip":
		out = framearc.FlipBit(arc, *off, *bit)
	case "wrong-table":
		out, err = framearc.WrongTable(arc, *frame)
		check(err)
	default:
		fmt.Fprintln(os.Stderr, "unknown -mode:", *mode)
		os.Exit(2)
	}
	_, err = os.Stdout.Write(out)
	check(err)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "framearc:", err)
		os.Exit(1)
	}
}
