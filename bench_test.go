package floatconv_test

import (
	"encoding/binary"
	"math/rand"
	"testing"

	"floatconv"
)

const benchN = 1 << 16

func benchSrc32() []byte {
	rng := rand.New(rand.NewSource(1))
	src := make([]byte, benchN*4)
	for i := 0; i < benchN; i++ {
		binary.LittleEndian.PutUint32(src[i*4:], rng.Uint32())
	}
	return src
}

func BenchmarkConvertBatchBinary32ToBinary16(b *testing.B) {
	src := benchSrc32()
	dst := make([]byte, benchN*2)
	b.SetBytes(benchN * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := floatconv.ConvertBatch(dst, floatconv.Binary16, src, floatconv.Binary32, floatconv.RoundNearestEven); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvertBatchBinary16ToBinary32(b *testing.B) {
	src32 := benchSrc32()
	src := make([]byte, benchN*2)
	if _, err := floatconv.ConvertBatch(src, floatconv.Binary16, src32, floatconv.Binary32, floatconv.RoundNearestEven); err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, benchN*4)
	b.SetBytes(benchN * 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := floatconv.ConvertBatch(dst, floatconv.Binary32, src, floatconv.Binary16, floatconv.RoundNearestEven); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseDecimalBinary16(b *testing.B) {
	s := "3.14159265358979323846264338327950288e0"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := floatconv.ParseDecimal(floatconv.Binary16, s, floatconv.RoundNearestEven); err != nil {
			b.Fatal(err)
		}
	}
}
