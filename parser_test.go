package framemux

import (
	"bytes"
	"testing"
)

func encodeOne(f frame) []byte {
	return appendEscaped(nil, f.appendContent(nil), f.epoch)
}

func feedAll(p *parser, data []byte, chunk int) []parseEvent {
	var evs []parseEvent
	for len(data) > 0 {
		n := chunk
		if n <= 0 || n > len(data) {
			n = len(data)
		}
		evs = p.feed(data[:n], evs)
		data = data[n:]
	}
	return evs
}

func goodFrames(evs []parseEvent) []frame {
	var fs []frame
	for _, ev := range evs {
		if ev.err == nil {
			fs = append(fs, *ev.frame)
		}
	}
	return fs
}

func badCount(evs []parseEvent) int {
	n := 0
	for _, ev := range evs {
		if ev.err != nil {
			n++
		}
	}
	return n
}

func TestParserArbitraryChunking(t *testing.T) {
	frames := []frame{
		{epoch: 0, stream: 1, seq: 0, typ: typeData, payload: []byte("hello")},
		{epoch: 0, stream: 2, seq: 0, typ: typeData, payload: []byte{flagByte, escByte, 0x00, 0xFF}},
		{epoch: 0, stream: 1, seq: 1, typ: typeData, payload: bytes.Repeat([]byte{0xAB}, 300)},
	}
	var wire []byte
	for _, f := range frames {
		wire = append(wire, encodeOne(f)...)
	}
	for _, chunk := range []int{1, 2, 3, 5, 7, 64, 0} {
		p := newParser(DefaultMaxPayload)
		got := goodFrames(feedAll(p, wire, chunk))
		if len(got) != len(frames) {
			t.Fatalf("chunk=%d: got %d frames, want %d", chunk, len(got), len(frames))
		}
		for i, f := range frames {
			if !(got[i].stream == f.stream && got[i].seq == f.seq &&
				got[i].typ == f.typ && bytes.Equal(got[i].payload, f.payload)) {
				t.Fatalf("chunk=%d frame %d mismatch: %+v", chunk, i, got[i])
			}
		}
		if badCount(feedAll(newParser(DefaultMaxPayload), wire, chunk)) != 0 {
			t.Fatalf("chunk=%d: unexpected bad data", chunk)
		}
	}
}

func TestParserEscapeSplitAcrossFeeds(t *testing.T) {
	f := frame{epoch: 0, stream: 7, seq: 3, typ: typeData, payload: []byte{0x01, flagByte, 0x02}}
	wire := encodeOne(f)
	// Find an escape introducer and split the feed right after it.
	idx := bytes.IndexByte(wire, escByte)
	if idx < 0 || idx+1 >= len(wire) {
		t.Fatal("no split-able escape in wire bytes")
	}
	p := newParser(DefaultMaxPayload)
	if evs := p.feed(wire[:idx+1], nil); len(evs) != 0 {
		t.Fatalf("events before frame complete: %+v", evs)
	}
	if !p.escPending {
		t.Fatal("escape state not preserved across feeds")
	}
	evs := p.feed(wire[idx+1:], nil)
	got := goodFrames(evs)
	if len(got) != 1 || !bytes.Equal(got[0].payload, f.payload) {
		t.Fatalf("got %+v", got)
	}
}

func TestParserAllByteValuesPayload(t *testing.T) {
	var payload [256]byte
	for i := range payload {
		payload[i] = byte(i)
	}
	f := frame{epoch: 1, stream: 9, seq: 42, typ: typeData, payload: payload[:]}
	p := newParser(DefaultMaxPayload)
	got := goodFrames(feedAll(p, encodeOne(f), 1))
	if len(got) != 1 || !bytes.Equal(got[0].payload, payload[:]) {
		t.Fatalf("roundtrip of all 256 byte values failed: %+v", got)
	}
}

func TestParserBadCRCResync(t *testing.T) {
	bad := encodeOne(frame{epoch: 0, stream: 1, seq: 0, typ: typeData, payload: []byte("corrupt me")})
	bad[len(bad)/2] ^= 0xFF // flip a content byte, keep flags intact
	good := encodeOne(frame{epoch: 0, stream: 1, seq: 1, typ: typeData, payload: []byte("still good")})
	p := newParser(DefaultMaxPayload)
	evs := feedAll(p, append(bad, good...), 0)
	if badCount(evs) != 1 {
		t.Fatalf("want 1 bad-data event, got %d", badCount(evs))
	}
	got := goodFrames(evs)
	if len(got) != 1 || string(got[0].payload) != "still good" {
		t.Fatalf("resync failed: %+v", got)
	}
}

func TestParserDanglingEscape(t *testing.T) {
	// FLAG, escape introducer, FLAG: the escape has no successor byte.
	wire := []byte{flagByte, escByte, flagByte}
	good := encodeOne(frame{epoch: 0, stream: 3, seq: 0, typ: typeData, payload: []byte("after")})
	p := newParser(DefaultMaxPayload)
	evs := feedAll(p, append(wire, good...), 0)
	if badCount(evs) != 1 {
		t.Fatalf("want 1 bad-data event, got %d", badCount(evs))
	}
	got := goodFrames(evs)
	if len(got) != 1 || string(got[0].payload) != "after" {
		t.Fatalf("no resync after dangling escape: %+v", got)
	}
}

func TestParserLostDelimiterResync(t *testing.T) {
	f1 := encodeOne(frame{epoch: 0, stream: 1, seq: 0, typ: typeData, payload: []byte("first")})
	f2 := encodeOne(frame{epoch: 0, stream: 1, seq: 1, typ: typeData, payload: []byte("second")})
	f3 := encodeOne(frame{epoch: 0, stream: 1, seq: 2, typ: typeData, payload: []byte("third")})
	// Drop the flag between f1 and f2: their contents merge into one
	// oversized/corrupt frame that ends at f2's trailing flag.
	wire := append(f1[:len(f1)-1], f2[1:]...)
	wire = append(wire, f3...)
	p := newParser(DefaultMaxPayload)
	evs := feedAll(p, wire, 0)
	if badCount(evs) != 1 {
		t.Fatalf("want 1 bad-data event, got %d", badCount(evs))
	}
	got := goodFrames(evs)
	if len(got) != 1 || string(got[0].payload) != "third" {
		t.Fatalf("resync after lost delimiter failed: %+v", got)
	}
}

func TestParserPseudoMagicNotAFrame(t *testing.T) {
	// Payload full of flag-like and escape-like bytes must arrive as ONE
	// frame; none of the magic lookalikes may start a new frame.
	payload := bytes.Repeat([]byte{flagByte, escByte, flagByte ^ escXor}, 50)
	f := frame{epoch: 0, stream: 5, seq: 0, typ: typeData, payload: payload}
	p := newParser(DefaultMaxPayload)
	evs := feedAll(p, encodeOne(f), 3)
	got := goodFrames(evs)
	if len(got) != 1 || !bytes.Equal(got[0].payload, payload) {
		t.Fatalf("pseudo-magic payload split into %d frames", len(got))
	}
	if badCount(evs) != 0 {
		t.Fatal("pseudo-magic payload produced bad-data events")
	}
}

func TestParserOverlongInputBounded(t *testing.T) {
	p := newParser(DefaultMaxPayload)
	garbage := append([]byte{flagByte}, bytes.Repeat([]byte{'A'}, 1<<20)...)
	evs := p.feed(garbage, nil)
	if badCount(evs) != 1 {
		t.Fatalf("want 1 bad-data event, got %d", badCount(evs))
	}
	if p.BytesScanned != len(garbage) {
		t.Fatalf("scanned %d bytes for %d input bytes (work not O(n))", p.BytesScanned, len(garbage))
	}
	if p.MaxBuffered > p.maxContent {
		t.Fatalf("buffered %d bytes, max allowed %d", p.MaxBuffered, p.maxContent)
	}
	if len(p.content) != 0 || p.state != stateHunt {
		t.Fatal("parser did not fall back to bounded hunt state")
	}
	// And a valid frame afterwards still parses.
	good := encodeOne(frame{epoch: 0, stream: 1, seq: 0, typ: typeData, payload: []byte("ok")})
	if got := goodFrames(p.feed(good, nil)); len(got) != 1 {
		t.Fatalf("no parse after overflow: %+v", got)
	}
}

func TestParserDistinguishesNeedMoreFromBad(t *testing.T) {
	f := frame{epoch: 0, stream: 1, seq: 0, typ: typeData, payload: []byte("partial")}
	wire := encodeOne(f)
	p := newParser(DefaultMaxPayload)
	// Feeding a prefix yields neither frames nor errors: just need more data.
	for i := 1; i < len(wire); i++ {
		if evs := p.feed(wire[i-1:i], nil); len(evs) != 0 {
			t.Fatalf("prefix %d produced events: %+v", i, evs)
		}
	}
	if evs := p.feed(wire[len(wire)-1:], nil); len(evs) != 1 || evs[0].err != nil {
		t.Fatalf("final byte: %+v", evs)
	}
	// Corrupt data, by contrast, surfaces a *BadDataError.
	p2 := newParser(DefaultMaxPayload)
	evs := p2.feed([]byte{flagByte, 0xFF, 0xFF, 0xFF, flagByte}, nil)
	if len(evs) != 1 || evs[0].err == nil {
		t.Fatalf("corrupt frame not reported: %+v", evs)
	}
}
