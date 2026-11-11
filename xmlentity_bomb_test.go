package xmlentity

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
)

// buildExponentialBomb 构造经典的指数实体膨胀（"billion laughs" 家族）样本：
// e0 为固定字符串，e(i) 引用两次 e(i-1)，第 n 层完全展开后为 2^n 个 base。
// 输入本身只有 O(n) 个实体声明，理论膨胀后的体量却呈指数增长。
func buildExponentialBomb(levels int, base string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE r [")
	b.WriteString("<!ENTITY e0 \"" + base + "\">")
	for i := 1; i <= levels; i++ {
		b.WriteString("<!ENTITY e" + itoa(i) + " \"&e" + itoa(i-1) + ";&e" + itoa(i-1) + ";\">")
	}
	b.WriteString("]><r>&e" + itoa(levels) + ";</r>")
	return b.String()
}

// measurePeakHeap 以极小读取缓冲跑通读取器，采样期间的堆峰值。
// 读取器的正确性承诺是“不缓冲全文”，因此炸弹场景的堆增长应保持常数级，
// 与理论展开体量（2^levels * len(base)）无关。
func measurePeakHeap(t *testing.T, r *Reader) (output []byte, peak uint64, err error) {
	t.Helper()
	runtime.GC()
	var ms0, ms runtime.MemStats
	runtime.ReadMemStats(&ms0)

	buf := make([]byte, 64)
	var out bytes.Buffer
	for {
		n, rerr := r.Read(buf)
		out.Write(buf[:n])
		runtime.ReadMemStats(&ms)
		if heap := ms.HeapAlloc - ms0.HeapAlloc; heap > peak {
			peak = heap
		}
		if rerr == io.EOF {
			return out.Bytes(), peak, nil
		}
		if rerr != nil {
			return out.Bytes(), peak, rerr
		}
	}
}

// TestExponentialBombRejected 验证指数膨胀样本在很小的预算下被即时拒绝，
// 既不会产出 GB 级输出，也不会让堆内存随膨胀指数增长。
func TestExponentialBombRejected(t *testing.T) {
	// 28 层时理论展开为 2^28 * 10 ≈ 2.7 GB，而输入只有约 1 KB。
	const levels = 28
	bomb := buildExponentialBomb(levels, "0123456789")
	t.Logf("bomb input size = %d bytes, theoretical expansion = %d bytes",
		len(bomb), int64(1<<levels)*10)

	limits := Limits{
		MaxExpandedBytes:   1 << 20, // 1 MiB 展开上限
		MaxRefsPerEntity:   1000,
		MaxTotalEntityRefs: 100_000,
		MaxNestDepth:       64,
		MaxDTDBytes:        1 << 16,
	}
	r := NewReader(strings.NewReader(bomb), limits)
	out, peak, err := measurePeakHeap(t, r)

	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("expected a LimitError, got %v", err)
	}
	// 输出被预算截断，绝不可能接近理论膨胀体量。
	if int64(len(out)) > limits.MaxExpandedBytes {
		t.Fatalf("output %d bytes exceeds budget %d", len(out), limits.MaxExpandedBytes)
	}
	// 关键安全属性：堆峰值与 2.7 GB 的理论膨胀无关，保持在 KB 级常数范围。
	// 这里给一个宽松上界（4 MiB），覆盖运行时噪声但足以证明没有缓冲全文。
	const heapCeil = 4 << 20
	if peak > heapCeil {
		t.Fatalf("peak heap growth %d bytes exceeds %d: reader buffered expanded content", peak, heapCeil)
	}
	t.Logf("bomb: limit=%s, output=%d bytes, peak heap growth=%d bytes",
		le.Limit, len(out), peak)
}

// TestNormalDocumentMemoryAndOutput 用一份体量相近、结构正常的文档做对照：
// 同样的读取器应当完整产出正确结果，且内存同样平稳。
func TestNormalDocumentMemoryAndOutput(t *testing.T) {
	// 正常文档：单实体被重复引用，输出约 500 KB，引用次数远低于预算。
	const refs = 100_000
	var want bytes.Buffer
	want.WriteString(`<!DOCTYPE r [<!ENTITY word "hello">]>`)
	want.WriteString("<r>")
	for i := 0; i < refs; i++ {
		want.WriteString("hello")
	}
	want.WriteString("</r>")

	var input strings.Builder
	input.WriteString(`<!DOCTYPE r [<!ENTITY word "hello">]><r>`)
	for i := 0; i < refs; i++ {
		input.WriteString("&word;")
	}
	input.WriteString("</r>")

	limits := Limits{
		MaxExpandedBytes:   8 << 20,
		MaxRefsPerEntity:   refs + 10,
		MaxTotalEntityRefs: refs + 10,
		MaxNestDepth:       8,
		MaxDTDBytes:        1 << 16,
	}
	r := NewReader(strings.NewReader(input.String()), limits)
	out, peak, err := measurePeakHeap(t, r)
	if err != nil {
		t.Fatalf("normal document rejected: %v", err)
	}
	if !bytes.Equal(out, want.Bytes()) {
		t.Fatalf("normal document output mismatch: got %d bytes, want %d", len(out), want.Len())
	}
	// 正常文档的输出缓冲由测试自身的 bytes.Buffer 持有，读取器内部的额外
	// 堆增长仍应是常数级；这里只断言读取器没有出现二次方/指数级膨胀。
	const heapCeil = 8 << 20
	if peak > heapCeil {
		t.Fatalf("peak heap growth %d bytes exceeds %d", peak, heapCeil)
	}
	t.Logf("normal: input=%d bytes, output=%d bytes, peak heap growth=%d bytes",
		input.Len(), len(out), peak)
}

// TestBombVsNormalMemoryComparison 把两类样本放进同一个测试，直接对比
// 读取器在“炸弹被拒”和“正常文档完整展开”下的输出体量与堆峰值，
// 证明安全性不依赖输入是否善意，且正常功能不受影响。
func TestBombVsNormalMemoryComparison(t *testing.T) {
	bomb := buildExponentialBomb(24, "abcdefghij") // 理论 2^24 * 10 ≈ 167 MB
	bombReader := NewReader(strings.NewReader(bomb), Limits{
		MaxExpandedBytes: 256 << 10, MaxNestDepth: 64, MaxDTDBytes: 1 << 16,
	})
	bombOut, bombPeak, bombErr := measurePeakHeap(t, bombReader)

	normalIn := `<!DOCTYPE r [<!ENTITY w "abcdefghij">]><r>` +
		strings.Repeat("&w;", 30_000) + `</r>`
	normalPrefix := `<!DOCTYPE r [<!ENTITY w "abcdefghij">]>`
	normalReader := NewReader(strings.NewReader(normalIn), Limits{
		MaxExpandedBytes: 8 << 20, MaxRefsPerEntity: 40_000,
		MaxTotalEntityRefs: 40_000, MaxNestDepth: 8, MaxDTDBytes: 1 << 16,
	})
	normalOut, normalPeak, normalErr := measurePeakHeap(t, normalReader)

	if !errors.Is(bombErr, ErrLimitExceeded) {
		t.Fatalf("bomb not limited: %v", bombErr)
	}
	if normalErr != nil {
		t.Fatalf("normal document errored: %v", normalErr)
	}
	wantNormalLen := len(normalPrefix) + 30_000*10 + len("<r>") + len("</r>")
	if len(normalOut) != wantNormalLen {
		t.Fatalf("normal output size = %d", len(normalOut))
	}
	if !strings.HasPrefix(string(normalOut), normalPrefix+"<r>abcdefghij") {
		t.Fatalf("normal output corrupted: %q", normalOut[:min(32, len(normalOut))])
	}
	t.Logf("comparison: bomb output=%d peakHeap=%d (rejected) | normal output=%d peakHeap=%d (ok)",
		len(bombOut), bombPeak, len(normalOut), normalPeak)
}
