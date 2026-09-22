package bitlayout

import "testing"

func benchObject() map[string]any {
	samples := make([]any, 0, 8)
	for i := 0; i < 8; i++ {
		samples = append(samples, map[string]any{"v": uint64(i * 7 % 64)})
	}
	return map[string]any{
		"tag": uint64(1), "value": int64(-37),
		"body": map[string]any{"reading": uint64(2345)},
		"n":    uint64(8), "samples": samples,
	}
}

func BenchmarkCompile(b *testing.B) {
	s := telemetrySchema("msb", "big")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(s); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	p, err := Compile(telemetrySchema("msb", "big"))
	if err != nil {
		b.Fatal(err)
	}
	obj := benchObject()
	dst := make([]byte, 64)
	b.SetBytes(int64(len(dst)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Encode(obj, dst); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecode(b *testing.B) {
	p, err := Compile(telemetrySchema("msb", "big"))
	if err != nil {
		b.Fatal(err)
	}
	obj := benchObject()
	data, _, err := p.EncodeBits(obj)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := p.NewSession(Quotas{})
		s.Feed(data)
		if err := s.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
