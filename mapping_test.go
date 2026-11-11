package netsec

import "testing"

func authRanges(parts ...Range) []Range { return parts }

func trustedBytes(m *Mapping) []bool {
	out := make([]bool, len(m.Bytes))
	for i, b := range m.Bytes {
		out[i] = b.Trusted
	}
	return out
}

func equalBool(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 1. QP 软换行：'=' 与 CRLF 是传输框架，不产生任何解码字节，
// 也不得出现在任何依赖中；换行前后的可见字符正常拼接。
func TestQPSoftLineBreak(t *testing.T) {
	raw := []byte("foo=\r\nbar")
	m, err := MapPart(raw, EncodingQuotedPrintable, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Decoded) != "foobar" {
		t.Fatalf("decoded = %q, want %q", m.Decoded, "foobar")
	}
	if len(m.Bytes) != 6 {
		t.Fatalf("got %d decoded bytes, want 6", len(m.Bytes))
	}
	for _, b := range m.Bytes {
		for _, pos := range b.Deps {
			if raw[pos] == '=' || raw[pos] == '\r' || raw[pos] == '\n' {
				t.Fatalf("soft break framing byte at %d leaked into deps %v", pos, b.Deps)
			}
		}
	}

	m2, err := MapPart(raw, EncodingQuotedPrintable, authRanges(Range{Start: 3, End: 6}))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range m2.Bytes {
		if b.Trusted {
			t.Fatalf("framing-only authentication marked byte %d trusted", b.Decoded)
		}
	}
}

// 2. QP 十六进制转义是 3 原始字符 -> 1 解码字节的编码单元：
// 三依赖中任一未认证，该字节即不可信（半覆盖不可信）。
func TestQPHexEscapeHalfCoverage(t *testing.T) {
	raw := []byte("ab=E9z") // "=E9" 位于原始偏移 3..5

	// "=E9" 实际位于原始偏移 2..4（a=0, b=1, ==2）。
	full, err := MapPart(raw, EncodingQuotedPrintable, authRanges(Range{Start: 2, End: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if !full.Bytes[2].Trusted {
		t.Fatalf("fully covered escape should be trusted: %+v", full.Bytes[2])
	}
	wantDeps := []int64{2, 3, 4}
	if len(full.Bytes[2].Deps) != 3 {
		t.Fatalf("deps = %v, want %v", full.Bytes[2].Deps, wantDeps)
	}
	for i, pos := range wantDeps {
		if full.Bytes[2].Deps[i] != pos {
			t.Fatalf("deps = %v, want %v", full.Bytes[2].Deps, wantDeps)
		}
	}

	half, _ := MapPart(raw, EncodingQuotedPrintable, authRanges(Range{Start: 2, End: 4}))
	if half.Bytes[2].Trusted {
		t.Fatal("half-covered QP escape must be untrusted")
	}

	split, _ := MapPart(raw, EncodingQuotedPrintable, authRanges(
		Range{Start: 2, End: 3},
		Range{Start: 3, End: 5},
	))
	if !split.Bytes[2].Trusted {
		t.Fatal("escape fully covered by two adjacent ranges must be trusted")
	}
}

// 3. Base64 填充：4 字符量子内每个解码字节都依赖全部 4 个原始字符
// （含 '='），半覆盖量子整体不可信。
func TestBase64Padding(t *testing.T) {
	raw := []byte("Zm9vYg==") // -> "foob"；填充位于偏移 6、7

	full, err := MapPart(raw, EncodingBase64, authRanges(Range{Start: 0, End: 8}))
	if err != nil {
		t.Fatal(err)
	}
	if string(full.Decoded) != "foob" {
		t.Fatalf("decoded = %q, want foob", full.Decoded)
	}
	for _, b := range full.Bytes {
		if !b.Trusted {
			t.Fatalf("fully authenticated byte %d should be trusted", b.Decoded)
		}
	}

	// 未认证两个填充：最后一个量子半覆盖，其唯一解码字节 'b' 不可信。
	noPad, _ := MapPart(raw, EncodingBase64, authRanges(Range{Start: 0, End: 6}))
	want := []bool{true, true, true, false}
	if got := trustedBytes(noPad); !equalBool(got, want) {
		t.Fatalf("trusted = %v, want %v", got, want)
	}
	if got := noPad.Bytes[3].Deps; len(got) != 4 || got[2] != 6 || got[3] != 7 {
		t.Fatalf("last byte deps = %v, must include padding positions 6,7", got)
	}

	// 只漏一个数据字符：整个量子三个字节全部不可信。
	raw2 := []byte("Zm9v") // -> "foo"，漏掉偏移 3 的 'v'
	gap, _ := MapPart(raw2, EncodingBase64, authRanges(Range{Start: 0, End: 3}))
	for _, b := range gap.Bytes {
		if b.Trusted {
			t.Fatalf("half-covered quantum byte %d must be untrusted", b.Decoded)
		}
	}
}

// 4. Base64 截断：结尾不完整量子尽力输出，但强制不可信并记录 Issue，
// 即使现有符号恰好全部在已认证区间内也不允许高亮为可信。
func TestBase64Truncation(t *testing.T) {
	raw := []byte("Zm9") // 三个符号应解出 "fo"，结尾缺少第四个符号
	m, err := MapPart(raw, EncodingBase64, authRanges(Range{Start: 0, End: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if string(m.Decoded) != "fo" {
		t.Fatalf("decoded = %q, want fo", m.Decoded)
	}
	for _, b := range m.Bytes {
		if b.Trusted {
			t.Fatalf("truncated quantum byte %d must be untrusted", b.Decoded)
		}
	}
	if len(m.Issues) != 1 || m.Issues[0].Kind != IssueTruncated {
		t.Fatalf("issues = %+v, want one truncated issue", m.Issues)
	}

	// 单个悬空符号无法产出字节，按标准库视为损坏。
	one, _ := MapPart([]byte("Z"), EncodingBase64, authRanges(Range{Start: 0, End: 1}))
	if len(one.Decoded) != 0 || len(one.Issues) != 1 || one.Issues[0].Kind != IssueMalformed {
		t.Fatalf("single dangling symbol: %+v", one)
	}
}

// 5. 跨片段覆盖与量子边界：量子落在两个已认证区间接缝处时，
// 只有 4 个字符全部被某段（或多段拼接）覆盖才可信。
func TestBase64CrossFragment(t *testing.T) {
	raw := []byte("Zm9vYg==") // 两个量子，接缝在偏移 4

	// 两段交叠覆盖全部 8 个原始位置：两个量子均可信。
	m, _ := MapPart(raw, EncodingBase64, authRanges(
		Range{Start: 0, End: 5},
		Range{Start: 4, End: 8},
	))
	if string(m.Decoded) != "foob" {
		t.Fatalf("decoded = %q", m.Decoded)
	}
	for _, b := range m.Bytes {
		if !b.Trusted {
			t.Fatalf("byte %d untrusted despite full cross-fragment coverage", b.Decoded)
		}
	}

	// 接缝切在第二个量子中间（偏移 5 未覆盖）：
	// 第一个量子可信，第二个量子整体不可信。
	gap, _ := MapPart(raw, EncodingBase64, authRanges(
		Range{Start: 0, End: 5},
		Range{Start: 6, End: 8},
	))
	want := []bool{true, true, true, false}
	if got := trustedBytes(gap); !equalBool(got, want) {
		t.Fatalf("trusted = %v, want %v", got, want)
	}
}

// 6. CR/LF 折行不属于任何量子的依赖：只认证数据字符时仍可信；
// 仅认证折行字符不产生任何可信正文；空格属于非法字符。
func TestBase64LineBreaksAreFraming(t *testing.T) {
	raw := []byte("Zm9v\r\nYg==") // CR/LF 在偏移 4、5
	m, _ := MapPart(raw, EncodingBase64, authRanges(
		Range{Start: 0, End: 4},
		Range{Start: 6, End: 10},
	))
	if string(m.Decoded) != "foob" {
		t.Fatalf("decoded = %q, want foob", m.Decoded)
	}
	for _, b := range m.Bytes {
		if !b.Trusted {
			t.Fatalf("byte %d should be trusted without authenticating CRLF", b.Decoded)
		}
		for _, pos := range b.Deps {
			if raw[pos] == '\r' || raw[pos] == '\n' {
				t.Fatalf("line break at %d leaked into deps", pos)
			}
		}
	}

	onlyBreaks, _ := MapPart(raw, EncodingBase64, authRanges(Range{Start: 4, End: 6}))
	for _, b := range onlyBreaks.Bytes {
		if b.Trusted {
			t.Fatal("authenticating line breaks alone must not trust content")
		}
	}

	// 空格不是合法 Base64 字符：记录损坏并停止解码。
	bad, _ := MapPart([]byte("Zm 9v"), EncodingBase64, nil)
	if string(bad.Decoded) != "" || len(bad.Issues) != 1 || bad.Issues[0].Kind != IssueMalformed {
		t.Fatalf("space in base64: decoded=%q issues=%+v", bad.Decoded, bad.Issues)
	}
}

// 7. Identity 编码逐字节同偏移映射。
func TestIdentity(t *testing.T) {
	raw := []byte("hello world")
	m, _ := MapPart(raw, EncodingIdentity, authRanges(
		Range{Start: 0, End: 5},
		Range{Start: 6, End: 7},
	))
	if string(m.Decoded) != string(raw) {
		t.Fatal("identity must pass bytes through")
	}
	want := []bool{true, true, true, true, true, false, true, false, false, false, false}
	if got := trustedBytes(m); !equalBool(got, want) {
		t.Fatalf("trusted = %v, want %v", got, want)
	}
	for i, b := range m.Bytes {
		if len(b.Deps) != 1 || b.Deps[0] != int64(i) {
			t.Fatalf("identity byte %d deps = %v", i, b.Deps)
		}
	}
}

// 8. 多字节 UTF-8 字符跨认证边界时整个 rune 降级为不可信，
// 高亮区间不得切开一个可见字符。
func TestRuneRangesNoSplitRune(t *testing.T) {
	raw := []byte{0x61, 0xC3, 0xA9} // "aé"，é 为 2 字节
	m, _ := MapPart(raw, EncodingIdentity, authRanges(Range{Start: 0, End: 2}))
	spans := m.RuneRanges()
	want := []RuneSpan{
		{Start: 0, End: 1, Trusted: true},
		{Start: 1, End: 3, Trusted: false},
	}
	if len(spans) != len(want) {
		t.Fatalf("spans = %+v, want %+v", spans, want)
	}
	for i := range want {
		if spans[i] != want[i] {
			t.Fatalf("span %d = %+v, want %+v", i, spans[i], want[i])
		}
	}

	full, _ := MapPart(raw, EncodingIdentity, authRanges(Range{Start: 0, End: 3}))
	all := full.RuneRanges()
	if len(all) != 1 || !all[0].Trusted || all[0].End != 3 {
		t.Fatalf("fully trusted spans = %+v", all)
	}
}

// 9. 非法参数：越界区间与未知编码必须报错。
func TestInvalidInputs(t *testing.T) {
	if _, err := MapPart([]byte("abc"), EncodingIdentity, authRanges(Range{Start: 1, End: 4})); err == nil {
		t.Fatal("out-of-bounds range must error")
	}
	if _, err := MapPart([]byte("abc"), EncodingIdentity, authRanges(Range{Start: 2, End: 1})); err == nil {
		t.Fatal("inverted range must error")
	}
	if _, err := MapPart([]byte("abc"), Encoding(99), nil); err == nil {
		t.Fatal("unknown encoding must error")
	}
}
