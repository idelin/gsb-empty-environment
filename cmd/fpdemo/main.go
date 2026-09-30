// fpdemo exercises the fpcodec library end to end: bit-exact encoding in
// both endiannesses, chunked decoding, text display, and format conversion
// with exception flags.
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"

	"fpcodec"
)

func main() {
	// A spread of 16-bit patterns: normal, -0, infinities, subnormal,
	// quiet and signaling NaN with payloads.
	vals := []uint64{0x3C00, 0x8000, 0x7C00, 0xFC00, 0x0001, 0x7E00, 0x7C01, 0x4248}

	fmt.Println("== text display (separate from storage) ==")
	for _, v := range vals {
		fmt.Printf("  bits=0x%04x  %s\n", v, fpcodec.Text(fpcodec.Width16, v))
	}

	fmt.Println("== encode little-endian vs big-endian ==")
	le, _ := fpcodec.Marshal(fpcodec.Width16, fpcodec.Little, vals)
	be, _ := fpcodec.Marshal(fpcodec.Width16, fpcodec.Big, vals)
	fmt.Printf("  LE: %s\n", hex.EncodeToString(le))
	fmt.Printf("  BE: %s\n", hex.EncodeToString(be))

	fmt.Println("== decode restores identical bits ==")
	_, gotLE, err := fpcodec.Decode(bytes.NewReader(le))
	check(err)
	_, gotBE, err := fpcodec.Decode(bytes.NewReader(be))
	check(err)
	fmt.Printf("  LE round trip bit-exact: %v\n", fpcodec.AllBitsEqual(fpcodec.Width16, vals, gotLE))
	fmt.Printf("  BE round trip bit-exact: %v\n", fpcodec.AllBitsEqual(fpcodec.Width16, vals, gotBE))
	fmt.Printf("  NaN payload preserved:   0x%04x -> 0x%04x\n", vals[6], gotLE[6])
	fmt.Printf("  -0 sign preserved:       0x%04x -> %s\n", gotLE[1], fpcodec.Text(fpcodec.Width16, gotLE[1]))

	fmt.Println("== format conversion with exception flags ==")
	pol := fpcodec.DefaultPolicy()
	demos := []struct {
		name string
		bits uint64
		from fpcodec.Width
	}{
		{"float32 1.5", uint64(math.Float32bits(1.5)), fpcodec.Width32},
		{"float32 1e30 (overflow)", uint64(math.Float32bits(1e30)), fpcodec.Width32},
		{"float32 1e-8 (underflow)", uint64(math.Float32bits(1e-8)), fpcodec.Width32},
		{"float32 0.1 (inexact)", uint64(math.Float32bits(0.1)), fpcodec.Width32},
		{"float32 sNaN payload=1", 0x7F800001, fpcodec.Width32},
		{"float64 qNaN payload", 0x7FF8000000001234, fpcodec.Width64},
	}
	for _, d := range demos {
		out, flags, err := fpcodec.Convert(d.bits, d.from, fpcodec.Width16, pol)
		check(err)
		fmt.Printf("  %-24s -> f16 0x%04x  %-18s %s\n",
			d.name, out, flags, fpcodec.Text(fpcodec.Width16, out))
	}

	fmt.Println("== flush-to-zero policy (explicit opt-in) ==")
	flush := fpcodec.Policy{
		NaNPayload: fpcodec.NaNTruncateMSB,
		Signaling:  fpcodec.SignalingQuiet,
		Subnormal:  fpcodec.SubnormalFlushToZero,
	}
	out, flags, _ := fpcodec.Convert(uint64(math.Float32bits(1e-6)), fpcodec.Width32, fpcodec.Width16, flush)
	fmt.Printf("  float32 1e-6 -> f16 0x%04x flags=%v\n", out, flags)
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
