// Command wiretool decodes, dumps and re-encodes wire messages.
//
//	wiretool [flags] [input-file]      (default input: stdin)
//
// Flags:
//
//	-mode decode|roundtrip   decode dumps fields; roundtrip also re-encodes
//	                         and verifies byte-identical output
//	-schema "5:msg,6:pvarint,7:p32,8:p64"
//	                         declare length-delimited fields as nested
//	                         messages or packed arrays (default: raw)
//	-max-total N             total input byte limit (0 = unlimited)
//	-max-field-len N         single field payload limit (0 = unlimited)
//	-max-fields N            field count limit (0 = unlimited)
//	-max-depth N             nested message depth limit (0 = unlimited)
//	-chunk N                 feed chunk size in bytes (default 4096)
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"wireparse"
)

func parseSchema(s string) (wire.Schema, error) {
	schema := wire.Schema{}
	if s == "" {
		return schema, nil
	}
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad schema entry %q, want field:kind", part)
		}
		num, err := strconv.Atoi(kv[0])
		if err != nil || num < 1 || num > wire.MaxFieldNumber {
			return nil, fmt.Errorf("bad field number in %q", part)
		}
		switch kv[1] {
		case "msg":
			schema[num] = wire.LengthField{Kind: wire.KindMessage}
		case "pvarint":
			schema[num] = wire.LengthField{Kind: wire.KindPacked, ElemWire: wire.WireVarint}
		case "p64":
			schema[num] = wire.LengthField{Kind: wire.KindPacked, ElemWire: wire.WireFixed64}
		case "p32":
			schema[num] = wire.LengthField{Kind: wire.KindPacked, ElemWire: wire.WireFixed32}
		case "raw":
			schema[num] = wire.LengthField{Kind: wire.KindRaw}
		default:
			return nil, fmt.Errorf("unknown kind in %q (msg|pvarint|p64|p32|raw)", part)
		}
	}
	return schema, nil
}

func dump(m *wire.Message, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, f := range m.Fields {
		switch f.Wire {
		case wire.WireVarint:
			fmt.Printf("%sfield %d varint %d\n", indent, f.Num, f.U64)
		case wire.WireFixed64:
			fmt.Printf("%sfield %d fixed64 %d\n", indent, f.Num, f.F64)
		case wire.WireFixed32:
			fmt.Printf("%sfield %d fixed32 %d\n", indent, f.Num, f.F32)
		case wire.WireBytes:
			switch f.Kind {
			case wire.KindMessage:
				fmt.Printf("%sfield %d message:\n", indent, f.Num)
				dump(f.Msg, depth+1)
			case wire.KindPacked:
				fmt.Printf("%sfield %d packed(wire=%d) %v\n", indent, f.Num, f.PackedWire, f.Packed)
			default:
				fmt.Printf("%sfield %d bytes[%d] %x\n", indent, f.Num, len(f.Raw), f.Raw)
			}
		}
	}
}

func main() {
	var (
		mode        = flag.String("mode", "decode", "decode|roundtrip")
		schemaFlag  = flag.String("schema", "", "field:kind comma list")
		maxTotal    = flag.Int64("max-total", 0, "total input byte limit")
		maxFieldLen = flag.Int64("max-field-len", 0, "single field payload limit")
		maxFields   = flag.Int("max-fields", 0, "field count limit")
		maxDepth    = flag.Int("max-depth", 0, "nested message depth limit")
		chunk       = flag.Int("chunk", 4096, "feed chunk size in bytes")
	)
	flag.Parse()

	schema, err := parseSchema(*schemaFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wiretool:", err)
		os.Exit(2)
	}
	limits := wire.Limits{
		MaxTotalBytes: *maxTotal,
		MaxFieldLen:   *maxFieldLen,
		MaxFields:     *maxFields,
		MaxDepth:      *maxDepth,
	}

	var in io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, "wiretool:", err)
			os.Exit(2)
		}
		defer f.Close()
		in = f
	}

	// Sample heap usage while parsing to record a peak.
	var peakHeap uint64
	done := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
				runtime.ReadMemStats(&ms)
				for {
					cur := atomic.LoadUint64(&peakHeap)
					if ms.HeapAlloc <= cur || atomic.CompareAndSwapUint64(&peakHeap, cur, ms.HeapAlloc) {
						break
					}
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()

	msg, stats, err := wire.Decode(in, limits, schema, *chunk)
	close(done)
	{
		var fms runtime.MemStats
		runtime.ReadMemStats(&fms)
		for {
			cur := atomic.LoadUint64(&peakHeap)
			if fms.HeapAlloc <= cur || atomic.CompareAndSwapUint64(&peakHeap, cur, fms.HeapAlloc) {
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wiretool:", err)
		os.Exit(1)
	}

	dump(msg, 0)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(os.Stderr,
		"stats: fields=%d messages=%d bytes=%d peak_buffer=%d peak_heap=%d total_alloc=%d\n",
		stats.Fields, stats.Messages, stats.BytesConsumed,
		stats.PeakBufferBytes, atomic.LoadUint64(&peakHeap), ms.TotalAlloc)

	if *mode == "roundtrip" {
		// Re-read the input to compare against the re-encoding.
		var orig []byte
		if flag.NArg() > 0 {
			orig, err = os.ReadFile(flag.Arg(0))
		} else {
			err = fmt.Errorf("roundtrip mode needs an input file")
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "wiretool:", err)
			os.Exit(2)
		}
		enc := msg.Encode()
		if !bytes.Equal(orig, enc) {
			fmt.Fprintf(os.Stderr, "wiretool: roundtrip MISMATCH: input %d bytes, re-encoded %d bytes\n", len(orig), len(enc))
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "roundtrip: OK (%d bytes identical)\n", len(orig))
	}
}
