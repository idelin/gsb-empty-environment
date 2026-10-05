// Command wiretool parses a length-untrusted binary message with the wire
// package, optionally dumps and re-encodes it, and reports resource peaks.
//
// Usage:
//
//	wiretool [-i file] [-schema schema.json] [-chunk N] [-dump]
//	         [-check] [-o file] [-max-total N] [-max-field-len N]
//	         [-max-fields N] [-max-depth N]
//
// The schema JSON declares which length-delimited fields are nested
// messages or packed arrays, e.g.:
//
//	{"nested": {"4": {"packed": {"1": 5}}}, "packed": {"7": 0}}
//
// means: field 4 is a nested message whose field 1 is a packed fixed32
// array, and top-level field 7 is a packed varint array. Wire types:
// 0 = varint, 1 = fixed64, 5 = fixed32.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"wiregw/wire"
)

type jsonSchema struct {
	Nested map[uint32]*jsonSchema `json:"nested"`
	Packed map[uint32]int         `json:"packed"`
}

func toSchema(js *jsonSchema) (*wire.Schema, error) {
	if js == nil {
		return nil, nil
	}
	s := &wire.Schema{}
	if len(js.Nested) > 0 {
		s.Nested = make(map[uint32]*wire.Schema, len(js.Nested))
		for field, sub := range js.Nested {
			converted, err := toSchema(sub)
			if err != nil {
				return nil, err
			}
			s.Nested[field] = converted
		}
	}
	if len(js.Packed) > 0 {
		s.Packed = make(map[uint32]int, len(js.Packed))
		for field, typ := range js.Packed {
			switch typ {
			case wire.TypeVarint, wire.TypeFixed64, wire.TypeFixed32:
			default:
				return nil, fmt.Errorf("field %d: invalid packed element wire type %d", field, typ)
			}
			s.Packed[field] = typ
		}
	}
	return s, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "wiretool:", err)
	os.Exit(1)
}

func main() {
	var (
		input     = flag.String("i", "", "input file (default: stdin)")
		output    = flag.String("o", "", "write the re-encoded message to this file")
		schemaF   = flag.String("schema", "", "JSON schema declaring nested/packed fields")
		chunk     = flag.Int("chunk", 0, "feed input in chunks of N bytes (0 = all at once)")
		dump      = flag.Bool("dump", false, "print a text dump of the message")
		check     = flag.Bool("check", false, "verify that re-encoding reproduces the input")
		maxTotal  = flag.Int64("max-total", 1<<20, "total message byte limit (0 = unlimited)")
		maxField  = flag.Int64("max-field-len", 1<<16, "single length-delimited field limit (0 = unlimited)")
		maxFields = flag.Int("max-fields", 10000, "fields per message limit (0 = unlimited)")
		maxDepth  = flag.Int("max-depth", 16, "nesting depth limit (0 = unlimited)")
	)
	flag.Parse()

	var schema *wire.Schema
	if *schemaF != "" {
		raw, err := os.ReadFile(*schemaF)
		if err != nil {
			fatal(err)
		}
		var js jsonSchema
		if err := json.Unmarshal(raw, &js); err != nil {
			fatal(fmt.Errorf("schema: %w", err))
		}
		if schema, err = toSchema(&js); err != nil {
			fatal(err)
		}
	}

	var data []byte
	var err error
	if *input == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*input)
	}
	if err != nil {
		fatal(err)
	}

	// Sample the process heap in the background to record a peak.
	var peakAlloc int64
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Millisecond)
		defer t.Stop()
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				runtime.ReadMemStats(&ms)
				for {
					cur := atomic.LoadInt64(&peakAlloc)
					if int64(ms.Alloc) <= cur || atomic.CompareAndSwapInt64(&peakAlloc, cur, int64(ms.Alloc)) {
						break
					}
				}
			}
		}
	}()
	defer close(stop)

	lim := wire.Limits{
		MaxTotalBytes: *maxTotal,
		MaxFieldLen:   *maxField,
		MaxFields:     *maxFields,
		MaxDepth:      *maxDepth,
	}
	p := wire.NewParser(lim, schema)
	msg := &wire.Message{}
	collect := func() error {
		for {
			f, err := p.Next()
			if err == wire.ErrNeedMore {
				return nil
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			msg.Fields = append(msg.Fields, *f)
		}
	}
	size := *chunk
	if size <= 0 {
		size = len(data)
		if size == 0 {
			size = 1
		}
	}
	for off := 0; off < len(data); off += size {
		end := off + size
		if end > len(data) {
			end = len(data)
		}
		if err := p.Feed(data[off:end]); err != nil {
			fatal(err)
		}
		if err := collect(); err != nil {
			fatal(err)
		}
	}
	if err := p.Finish(); err != nil {
		fatal(err)
	}
	for {
		f, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fatal(err)
		}
		msg.Fields = append(msg.Fields, *f)
	}

	if *dump {
		if err := msg.WriteDump(os.Stdout); err != nil {
			fatal(err)
		}
	}

	enc := msg.Marshal()
	if *check {
		if !bytes.Equal(enc, data) {
			at := 0
			for at < len(enc) && at < len(data) && enc[at] == data[at] {
				at++
			}
			fatal(fmt.Errorf("re-encode mismatch at byte %d (input %d bytes, re-encoded %d bytes)",
				at, len(data), len(enc)))
		}
		fmt.Fprintf(os.Stderr, "check: re-encode reproduces input (%d bytes)\n", len(data))
	}
	if *output != "" {
		if err := os.WriteFile(*output, enc, 0o644); err != nil {
			fatal(err)
		}
	}

	st := p.Stats()
	fmt.Fprintf(os.Stderr, "stats: bytes_fed=%d fields=%d nested_messages=%d peak_buffered=%d bytes_allocated=%d\n",
		st.BytesFed, st.FieldsParsed, st.MessagesParsed, st.PeakBuffered, st.BytesAllocated)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if int64(ms.Alloc) > atomic.LoadInt64(&peakAlloc) {
		atomic.StoreInt64(&peakAlloc, int64(ms.Alloc))
	}
	fmt.Fprintf(os.Stderr, "mem: alloc=%d total_alloc=%d heap_sys=%d peak_alloc_sampled=%d\n",
		ms.Alloc, ms.TotalAlloc, ms.HeapSys, atomic.LoadInt64(&peakAlloc))
}
