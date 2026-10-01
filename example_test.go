package strdict_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"

	"strdict"
)

// Encode a batch that mixes references, inline strings and a mid-stream
// dictionary version switch, then decode it under a byte budget.
func Example() {
	reg := strdict.NewRegistry()

	v1 := strdict.NewDictionary(1)
	_, _ = v1.Intern("red")
	_, _ = v1.Intern("green")
	v1.Freeze()
	if err := reg.Publish(v1); err != nil {
		log.Fatal(err)
	}

	// Generation 2 reuses index 0 for a different word.
	v2 := strdict.NewDictionary(2)
	_, _ = v2.Intern("green")
	_, _ = v2.Intern("blue")
	v2.Freeze()
	if err := reg.Publish(v2); err != nil {
		log.Fatal(err)
	}

	var buf bytes.Buffer
	enc := strdict.NewEncoder(&buf, v1)
	_ = enc.Ref(0)          // "red" from generation 1
	_ = enc.Inline("plain") // not pooled: written inline
	_ = enc.SwitchTo(v2)    // explicit switch point
	_ = enc.Ref(0)          // "green" from generation 2 (same index, new meaning)
	_ = enc.End()

	dec, err := strdict.NewDecoder(bytes.NewReader(buf.Bytes()), strdict.DecoderConfig{
		Registry: reg,
		Version:  1, // session pinned to generation 1 until the switch frame
		Budget:   strdict.NewBudget(1 << 20),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer dec.Close()
	for {
		s, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(s)
	}

	// Output:
	// red
	// plain
	// green
}
