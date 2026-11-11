package canon

import (
	"testing"
)

func FuzzDecodeRoundTrip(f *testing.F) {
	seeds := [][]byte{
		{0x00},
		{0x20},
		{0x40},
		{0x60},
		{0x80},
		{0xa0},
		{0x18, 0x18},
		{0x83, 0x01, 0x82, 0x02, 0x03, 0x82, 0x04, 0x05},
		{0xa2, 0x01, 0x02, 0x61, 0x61, 0x03},
		{0x44, 0x01, 0x02, 0x03, 0x04},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		value, err := Decode(data)
		if err != nil {
			return
		}
		reencoded, err := Encode(value)
		if err != nil {
			t.Fatalf("accepted value cannot be re-encoded: %v", err)
		}
		if !bytesEqual(reencoded, data) {
			t.Fatalf("non-unique accepted encoding:\n input: %x\n output: %x", data, reencoded)
		}
	})
}
