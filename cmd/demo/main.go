// Command demo exercises the wireint wire format and JSON bridge.
package main

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"

	"example.com/wireint"
)

func main() {
	fmt.Println("== 1. Canonical wire round-trip (protocol V2) ==")
	maxU64 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	pow100 := new(big.Int).Lsh(big.NewInt(1), 100)
	vals := []*big.Int{
		big.NewInt(-129), big.NewInt(-1), big.NewInt(0), big.NewInt(127),
		big.NewInt(128), big.NewInt(255), maxU64, pow100, new(big.Int).Neg(pow100),
	}
	for _, v := range vals {
		var buf bytes.Buffer
		if err := wireint.Encode(&buf, v, wireint.Version2); err != nil {
			fmt.Println("encode error:", err)
			continue
		}
		got, err := wireint.Decode(bytes.NewReader(buf.Bytes()), wireint.MaxVersion)
		status := "OK"
		if err != nil || got.Cmp(v) != 0 {
			status = "FAIL"
		}
		fmt.Printf("  %-42s wire=%-28x decoded=%v [%s]\n", v, buf.Bytes(), got, status)
	}

	fmt.Println("\n== 2. Multi-version protocol ==")
	var buf bytes.Buffer
	err := wireint.Encode(&buf, pow100, wireint.Version1)
	fmt.Printf("  bigint at V1 rejected: %v\n", err)
	wire := new(bytes.Buffer)
	_ = wireint.Encode(wire, pow100, wireint.Version2)
	_, err = wireint.Decode(bytes.NewReader(wire.Bytes()), wireint.Version1)
	fmt.Printf("  V2 tag read by V1-only decoder rejected: %v\n", err)

	fmt.Println("\n== 3. Streaming JSON bridge (string tags, no 53-bit float) ==")
	fields := []wireint.Field{wireint.Uint8, wireint.Int16, wireint.Uint64, wireint.Big(400)}
	in := `["iw:u8:200", "iw:i16:-300", "iw:u64:18446744073709551615", 1e100]`
	fmt.Printf("  input:  %s\n", in)
	got, err := wireint.DecodeJSONStream(strings.NewReader(in), fields)
	if err != nil {
		fmt.Println("  decode error:", err)
		return
	}
	for i, v := range got {
		fmt.Printf("  field %-8s raw=%-22q exact=%v\n", v.Field.Kind, v.Raw, v.Int)
		_ = i
	}
	var out bytes.Buffer
	_ = wireint.EncodeJSONStream(&out, got)
	fmt.Printf("  output: %s\n", out.String())

	fmt.Println("\n== 4. Rejections ==")
	nonCanonical := []byte{0x80 | 2<<4 | 7, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF} // -1 as int64
	_, err = wireint.Decode(bytes.NewReader(nonCanonical), wireint.MaxVersion)
	fmt.Printf("  non-canonical sign extension (-1 as i64): %v\n", err)
	_, err = wireint.DecodeJSONStream(strings.NewReader(`["iw:u8:-1"]`), []wireint.Field{wireint.Uint8})
	fmt.Printf("  negative into unsigned field:           %v\n", err)
	_, err = wireint.DecodeJSONStream(strings.NewReader(`[1e999999999]`), []wireint.Field{wireint.Big(256)})
	fmt.Printf("  huge exponent rejected by quota:        %v\n", err)
	_, err = wireint.DecodeJSONStream(strings.NewReader(`[1.5]`), []wireint.Field{wireint.Int64})
	fmt.Printf("  non-integer lexeme into int field:      %v\n", err)
}
