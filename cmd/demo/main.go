// Demo: versioned shared-string dictionary with streaming encode/decode.
package main

import (
	"fmt"

	"strdictdemo/strdict"
)

func main() {
	reg := strdict.NewRegistry()

	// Generation 1: freeze a dictionary.
	b1 := strdict.NewBuilder()
	b1.Add("hello")
	b1.Add("world")
	b1.Add("") // empty string is poolable
	v1 := reg.Publish(b1)

	// Generation 2: same indices, different meanings.
	b2 := strdict.NewBuilder()
	b2.Add("world")
	b2.Add("HELLO")
	b2.Add("")
	v2 := reg.Publish(b2)

	fmt.Printf("published v%d (%d entries, %d bytes) and v%d (%d entries, %d bytes)\n",
		v1.Version(), v1.Len(), v1.Bytes(), v2.Version(), v2.Len(), v2.Bytes())

	// Encode: refs + inline mixed, with an explicit switch point.
	enc := strdict.NewEncoder(v1, nil)
	enc.WriteString("hello")   // REF 0 @ v1
	enc.WriteString("inline!") // INLINE
	enc.WriteString("")        // REF 2 @ v1 (empty string)
	enc.Switch(v2)             // explicit version switch
	enc.WriteString("world")   // REF 0 @ v2
	enc.WriteString("HELLO")   // REF 1 @ v2
	enc.WriteEnd()
	stream := enc.Bytes()
	fmt.Printf("encoded %d bytes: % x\n", len(stream), stream)

	// Decode in 3-byte chunks, pinned to v1 at session start.
	dec, err := strdict.NewDecoder(reg, strdict.DecoderOptions{Version: v1.Version()})
	if err != nil {
		panic(err)
	}
	fmt.Println("decoding in 3-byte chunks:")
	for off := 0; off < len(stream); {
		n := 3
		if off+n > len(stream) {
			n = len(stream) - off
		}
		out, err := dec.Decode(stream[off : off+n])
		if err != nil {
			panic(err)
		}
		for _, s := range out {
			fmt.Printf("  -> %q\n", s)
		}
		off += n
	}
	dec.Close()

	// Byte budget: dictionary + staging + output. 8 bytes cannot even hold
	// the pinned dictionary (10 bytes), so the session is refused.
	_, err = strdict.NewDecoder(reg, strdict.DecoderOptions{Version: v1.Version(), MaxBytes: 8})
	fmt.Printf("budget 8 bytes (dict alone is %d): %v\n", v1.Bytes(), err)

	// Batch cancellation invalidates sessions and blocks new ones.
	reg.CancelAll()
	_, err = strdict.NewDecoder(reg, strdict.DecoderOptions{Version: v2.Version()})
	fmt.Printf("acquire after CancelAll: %v\n", err)
}
