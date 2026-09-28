// Command demo exercises the intwire library: canonical binary encoding in
// both protocol versions, the string-tagged JSON bridge, streaming, and
// rejection of non-canonical or hostile input.
package main

import (
	"bytes"
	"fmt"
	"math/big"

	"intwire-demo/intwire"
)

func main() {
	fmt.Println("== binary-binary round trip (v1 / v2) ==")
	u64max, _ := intwire.NewUnsigned(64, ^uint64(0))
	i16, _ := intwire.NewSigned(16, -12345)
	bigV := intwire.NewBig(new(big.Int).Lsh(big.NewInt(1), 200))
	for _, v := range []intwire.Value{u64max, i16, bigV} {
		for _, ver := range []intwire.Version{intwire.V1, intwire.V2} {
			buf, err := intwire.Append(nil, ver, v)
			if err != nil {
				panic(err)
			}
			got, err := intwire.NewDecoder(buf).Decode()
			if err != nil {
				panic(err)
			}
			fmt.Printf("v%d %-22s enc=%x... ok=%v\n", ver, v.Kind.String()+"="+v.Decimal(), truncate(buf, 24), got.Equal(v))
		}
	}

	fmt.Println("\n== JSON bridge (string tags, no 53-bit loss) ==")
	raw, _ := u64max.MarshalJSON()
	fmt.Printf("u64 max as JSON: %s\n", raw)
	var back intwire.Value
	if err := back.UnmarshalJSON(raw); err != nil {
		panic(err)
	}
	fmt.Printf("decoded exactly: %s (match=%v)\n", back.Decimal(), back.U == ^uint64(0))

	fmt.Println("\n== huge exponent lexeme: integer check + quota first, never float64 ==")
	v, err := intwire.ParseJSONValue([]byte(`1e400`))
	if err != nil {
		panic(err)
	}
	fmt.Printf("raw lexeme %q -> %d-bit exact integer, digits=%d\n", v.Raw, v.B.BitLen(), len(v.B.Text(10)))
	if _, err := intwire.ParseJSONValue([]byte(`1e999999999`)); err != nil {
		fmt.Printf("1e999999999 rejected before big math: %v\n", err)
	}
	if _, err := intwire.ParseJSONValue([]byte(`1.5`)); err != nil {
		fmt.Printf("1.5 rejected as non-integer: %v\n", err)
	}

	fmt.Println("\n== streaming bridge ==")
	var stream bytes.Buffer
	enc := intwire.NewStreamEncoder(&stream)
	for _, v := range []intwire.Value{u64max, i16, bigV} {
		if err := enc.WriteValue(v); err != nil {
			panic(err)
		}
	}
	dec := intwire.NewStreamDecoder(&stream)
	for n := 0; ; n++ {
		v, err := dec.ReadValue()
		if err != nil {
			break
		}
		fmt.Printf("stream[%d] kind=%s width=%d value=%s\n", n, v.Kind, v.Width, v.Decimal())
	}

	fmt.Println("\n== non-canonical encodings rejected, next field untouched ==")
	next, _ := intwire.Append(nil, intwire.V1, u64max)
	bad := []byte{byte(intwire.V1), 0x80, 0x02, 0x00, 0x7f} // +127 with redundant sign-extension byte
	d := intwire.NewDecoder(append(append([]byte{}, bad...), next...))
	if _, err := d.Decode(); err != nil {
		fmt.Printf("rejected: %v (decoder offset still %d)\n", err, d.Offset())
	}
	d2 := intwire.NewDecoder(d.Rest()[len(bad):])
	v2, _ := d2.Decode()
	fmt.Printf("following field decodes fine: %s\n", v2.Decimal())
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
