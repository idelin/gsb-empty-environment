package bitlayout_test

import (
	"testing"

	"bitlayout"
)

func BenchmarkValidateSensor(b *testing.B) {
	plan, _, _, rec := encodeSensor(&testing.T{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := plan.Validate(rec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeSensor(b *testing.B) {
	plan, _, _, rec := encodeSensor(&testing.T{})
	buf := make([]byte, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := plan.Encode(rec, buf); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodeSensor(b *testing.B) {
	plan, buf, nbits, _ := encodeSensor(&testing.T{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := plan.Decode(buf, nbits, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileSensor(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := bitlayout.Compile([]byte(sensorSchema), nil); err != nil {
			b.Fatal(err)
		}
	}
}
