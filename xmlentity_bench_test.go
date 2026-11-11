package xmlentity

import (
	"io"
	"strings"
	"testing"
)

func BenchmarkReaderNormal(b *testing.B) {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE r [<!ENTITY w "abcdefghij">]><r>`)
	for i := 0; i < 10_000; i++ {
		sb.WriteString("&w;")
	}
	sb.WriteString("</r>")
	input := sb.String()

	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewReader(strings.NewReader(input), Limits{
			MaxExpandedBytes: 16 << 20, MaxRefsPerEntity: 20_000, MaxTotalEntityRefs: 20_000,
		})
		if _, err := io.Copy(io.Discard, r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReaderBombStopped(b *testing.B) {
	// 28 层指数炸弹：测量“快速失败”路径的开销，应与正常文档同量级。
	bomb := buildExponentialBomb(28, "0123456789")
	b.SetBytes(int64(len(bomb)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := NewReader(strings.NewReader(bomb), Limits{
			MaxExpandedBytes: 1 << 20, MaxRefsPerEntity: 1000, MaxNestDepth: 64,
		})
		_, err := io.Copy(io.Discard, r)
		if err == nil {
			b.Fatal("expected budget error")
		}
	}
}
