// Command demo exercises the deltacodec codec end to end.
package main

import (
	"bytes"
	"fmt"
	"os"

	"deltacodec"
)

func main() {
	// Part 1: encode two channels; negative deltas, an explicit missing
	// sample, a reset, and a periodic anchor.
	var wire bytes.Buffer
	enc := deltacodec.NewEncoder(&wire)
	enc.SetAnchorPeriod(3)

	series := []struct {
		ch uint32
		v  int64
	}{
		{1, 100}, {2, -50}, {1, 103}, {1, 99}, {1, 120}, // ch1: anchor,delta,delta,anchor
		{2, -55}, {2, -55}, // negative and zero delta
	}
	for _, s := range series {
		must(enc.Encode(s.ch, s.v))
	}
	must(enc.EncodeMissing(1)) // sensor dropout; baseline retained
	must(enc.Encode(1, 125))   // delta against retained baseline
	must(enc.Reset(2))         // baseline discarded

	full := wire.Bytes()
	fmt.Println("== normal decode (chunk size 3) ==")
	dump(full, 3)

	// Part 2: drop one delta frame of channel 1. Every following delta for
	// that channel is undeterminable until an anchor arrives; channel 2 is
	// unaffected.
	frames := split(full)
	// frames[2] is channel 1's first delta (100 -> 103). Drop exactly that
	// frame to simulate loss on the wire.
	var kept [][]byte
	for i, f := range frames {
		if i != 2 {
			kept = append(kept, f)
		}
	}
	lossy := bytes.Join(kept, nil)
	fmt.Println("\n== after losing one channel-1 delta (note channel 2) ==")
	dump(lossy, 5)

	// Part 3: a fresh anchor repairs channel 1.
	var repair bytes.Buffer
	re := deltacodec.NewEncoder(&repair)
	must(re.EncodeAnchor(1, 500))
	fmt.Println("\n== anchor repairs channel 1 ==")
	dump(append(lossy, repair.Bytes()...), 16)

	// Part 4: corrupt one byte inside a frame. Bad data is fatal and must
	// never yield a guessed sample.
	bad := append([]byte(nil), full...)
	bad[len(bad)/2] ^= 0xFF
	fmt.Println("\n== corrupted byte on the wire ==")
	dec := deltacodec.NewDecoder()
	dec.Feed(bad)
	for {
		smp, err := dec.Decode()
		if err == deltacodec.ErrNeedMoreData {
			break
		}
		if err != nil {
			fmt.Printf("decoder stopped: %v (no guessed output)\n", err)
			break
		}
		fmt.Printf("  %s\n", render(smp))
	}
}

func dump(data []byte, chunk int) {
	dec := deltacodec.NewDecoder()
	for i := 0; i < len(data); {
		end := i + chunk
		if end > len(data) {
			end = len(data)
		}
		dec.Feed(data[i:end])
		i = end
		for {
			smp, err := dec.Decode()
			if err == deltacodec.ErrNeedMoreData {
				break
			}
			if err != nil {
				fmt.Printf("decoder stopped: %v\n", err)
				return
			}
			fmt.Printf("  %s\n", render(smp))
		}
	}
}

func render(s deltacodec.Sample) string {
	switch s.Status {
	case deltacodec.StatusOK:
		return fmt.Sprintf("ch%-2d value=%d", s.Channel, s.Value)
	case deltacodec.StatusMissing:
		return fmt.Sprintf("ch%-2d MISSING", s.Channel)
	default:
		return fmt.Sprintf("ch%-2d <unknown: undeterminable, no baseline>", s.Channel)
	}
}

// split cuts a frame stream into frames using the documented layout:
// tag, ch(uvarint), seq(uvarint), plen(uvarint), payload, crc32.
func split(data []byte) [][]byte {
	var out [][]byte
	rest := data
	for len(rest) > 0 {
		pos := 1
		pos += varLen(rest[pos:])
		pos += varLen(rest[pos:])
		pl := varLen(rest[pos:])
		plen := atouvarint(rest[pos : pos+pl])
		size := pos + pl + int(plen) + 4
		out = append(out, rest[:size])
		rest = rest[size:]
	}
	return out
}

func varLen(b []byte) int {
	for i, c := range b {
		if c&0x80 == 0 {
			return i + 1
		}
	}
	panic("short")
}

func atouvarint(b []byte) uint64 {
	var v uint64
	for i, c := range b {
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c&0x80 == 0 {
			break
		}
	}
	return v
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
