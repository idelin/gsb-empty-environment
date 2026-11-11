package xmlentity

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"encoding/xml"
)

// readAll 以极小的输出缓冲读取，强制覆盖流式分页与回退路径。
func readAll(t *testing.T, r io.Reader, bufSize int) ([]byte, error) {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, bufSize)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			return out.Bytes(), nil
		}
		if err != nil {
			return out.Bytes(), err
		}
	}
}

func runReader(t *testing.T, input string, limits Limits, bufSize int) ([]byte, error) {
	t.Helper()
	r := NewReader(strings.NewReader(input), limits)
	return readAll(t, r, bufSize)
}

// TestBasicExpansion 验证内部通用实体被即时展开，预定义/字符/未知实体保持原样。
func TestBasicExpansion(t *testing.T) {
	input := `<?xml version="1.0"?>
<!DOCTYPE root [
  <!ENTITY hello "world">
  <!ENTITY nested "&hello;&hello;">
  <!ELEMENT root ANY>
  <!ATTLIST root id CDATA #IMPLIED>
]>
<root id="&hello;">&hello; &nested; &amp; &#65; &unknown;</root>`

	want := `<?xml version="1.0"?>
<!DOCTYPE root [
  <!ENTITY hello "world">
  <!ENTITY nested "&hello;&hello;">
  <!ELEMENT root ANY>
  <!ATTLIST root id CDATA #IMPLIED>
]>
<root id="world">world worldworld &amp; &#65; &unknown;</root>`

	for _, bufSize := range []int{1, 3, 64, 4096} {
		got, err := runReader(t, input, Limits{}, bufSize)
		if err != nil {
			t.Fatalf("bufSize=%d: unexpected error: %v", bufSize, err)
		}
		if string(got) != want {
			t.Fatalf("bufSize=%d:\n got: %q\nwant: %q", bufSize, got, want)
		}
	}
}

// TestNoDoctype 验证没有 DTD 的文档完全透传，包括裸 '&'。
func TestNoDoctype(t *testing.T) {
	input := `<root>a & b && c&lt;&#65;</root>`
	got, err := runReader(t, input, Limits{}, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != input {
		t.Fatalf("got %q, want %q", got, input)
	}
}

// TestRejectExternalAndParameterEntities 覆盖所有必须拒绝的 DTD 构造。
func TestRejectExternalAndParameterEntities(t *testing.T) {
	cases := map[string]string{
		"external SYSTEM DTD":     `<!DOCTYPE root SYSTEM "http://evil.example/x.dtd"><root/>`,
		"external PUBLIC DTD":     `<!DOCTYPE root PUBLIC "x" "y.dtd"><root/>`,
		"external SYSTEM entity":  `<!DOCTYPE root [<!ENTITY x SYSTEM "file:///etc/passwd">]><root>&x;</root>`,
		"external PUBLIC entity":  `<!DOCTYPE root [<!ENTITY x PUBLIC "p" "u">]><root>&x;</root>`,
		"parameter declaration":   `<!DOCTYPE root [<!ENTITY % p "v">]><root/>`,
		"parameter reference":     `<!DOCTYPE root [<!ENTITY % p "v">%p;]><root/>`,
		"parameter in entity val": `<!DOCTYPE root [<!ENTITY x "%p;">]><root>&x;</root>`,
		"unparsed NDATA entity":   `<!DOCTYPE root [<!ENTITY x SYSTEM "x.gif" NDATA gif>]><root/>`,
		"NOTATION declaration":    `<!DOCTYPE root [<!NOTATION gif SYSTEM "image/gif">]><root/>`,
		"parameter in ATTLIST":    `<!DOCTYPE root [<!ATTLIST root a CDATA "%p;">]><root/>`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runReader(t, input, Limits{}, 16)
			if !errors.Is(err, ErrExternalEntity) {
				t.Fatalf("got %v, want ErrExternalEntity", err)
			}
		})
	}
}

// TestRecursiveEntities 验证直接与间接递归都被拒绝，且优先于深度/字节预算。
func TestRecursiveEntities(t *testing.T) {
	cases := map[string]string{
		"direct":   `<!DOCTYPE r [<!ENTITY a "&a;">]><r>&a;</r>`,
		"indirect": `<!DOCTYPE r [<!ENTITY a "&b;"><!ENTITY b "&a;">]><r>&a;</r>`,
		"cycle3":   `<!DOCTYPE r [<!ENTITY a "&b;"><!ENTITY b "&c;"><!ENTITY c "&a;">]><r>&a;</r>`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := runReader(t, input, Limits{}, 16)
			if !errors.Is(err, ErrRecursiveEntity) {
				t.Fatalf("got %v, want ErrRecursiveEntity", err)
			}
		})
	}
}

// TestNestDepthBudget 验证嵌套深度预算。
func TestNestDepthBudget(t *testing.T) {
	var dtd strings.Builder
	dtd.WriteString("<!DOCTYPE r [")
	dtd.WriteString(`<!ENTITY e0 "X">`)
	for i := 1; i <= 5; i++ {
		dtd.WriteString("<!ENTITY e" + itoa(i) + " \"&e" + itoa(i-1) + ";\">")
	}
	dtd.WriteString("]><r>&e5;</r>")

	// 深度 5 触发（引用 e5 -> e4 -> ... -> e0，最深实体帧为 6）。
	_, err := runReader(t, dtd.String(), Limits{MaxNestDepth: 5}, 8)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitNestDepth {
		t.Fatalf("got %v, want LimitNestDepth", err)
	}

	// 深度 6 刚好放行，输出单个 X。
	got, err := runReader(t, dtd.String(), Limits{MaxNestDepth: 6}, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(string(got), "<r>X</r>") {
		t.Fatalf("unexpected tail: %q", got)
	}
}

// TestRefsPerEntityBudget 验证单实体引用次数预算。
func TestRefsPerEntityBudget(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY a "abc">]><r>&a;&a;&a;</r>`
	_, err := runReader(t, input, Limits{MaxRefsPerEntity: 2}, 4)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitRefsPerEntity {
		t.Fatalf("got %v, want LimitRefsPerEntity", err)
	}

	got, err := runReader(t, input, Limits{MaxRefsPerEntity: 3}, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(string(got), "<r>abcabcabc</r>") {
		t.Fatalf("unexpected output: %q", got)
	}
}

// TestTotalRefsBudget 验证全文实体引用总次数预算。
func TestTotalRefsBudget(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY a "x"><!ENTITY b "y">]><r>&a;&b;&a;</r>`
	_, err := runReader(t, input, Limits{MaxTotalEntityRefs: 2, MaxRefsPerEntity: 100}, 4)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitTotalEntityRefs {
		t.Fatalf("got %v, want LimitTotalEntityRefs", err)
	}
}

// TestExpandedBytesBudget 验证展开总字节预算在流式读取中即时触发。
func TestExpandedBytesBudget(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY a "0123456789">]><r>&a;&a;&a;</r>`
	out, err := runReader(t, input, Limits{MaxExpandedBytes: 50}, 1)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitExpandedBytes {
		t.Fatalf("got %v, want LimitExpandedBytes", err)
	}
	// 预算覆盖全部输出字节（含原样透传的 DOCTYPE）。
	// DTD 片段占 40 字节，随后第一条实体展开 10 字节即触及 50 字节上限。
	if len(out) != 50 {
		t.Fatalf("output before error = %d bytes, want 50 (budget cap)", len(out))
	}
	if !bytes.HasPrefix(out, []byte(`<!DOCTYPE r [<!ENTITY a "0123456789">]><r>`)) {
		t.Fatalf("unexpected prefix: %q", out)
	}
	if !strings.HasSuffix(string(out), "01234567") {
		t.Fatalf("expansion should stop mid-entity at budget, got %q", out)
	}
	// 错误必须是可重复读取的终止状态，且不再消费输入。
	r := NewReader(strings.NewReader(input), Limits{MaxExpandedBytes: 50})
	_, _ = io.Copy(io.Discard, r)
	if !errors.Is(r.err, ErrLimitExceeded) {
		t.Fatalf("reader terminal error = %v", r.err)
	}
}

// TestDTDBytesBudget 验证内部 DTD 子集大小预算。
func TestDTDBytesBudget(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY a "01234567890123456789">]><r>&a;</r>`
	_, err := runReader(t, input, Limits{MaxDTDBytes: 20}, 4)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitDTDBytes {
		t.Fatalf("got %v, want LimitDTDBytes", err)
	}
}

// TestDanglingAndBareAmpersand 验证未闭合引用与裸 & 的透传行为。
func TestDanglingAndBareAmpersand(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY ab "X">]><r>&a &ab; &amp no-semicolon</r>`
	got, err := runReader(t, input, Limits{}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTail := "<r>&a X &amp no-semicolon</r>"
	if !strings.HasSuffix(string(got), wantTail) {
		t.Fatalf("got tail %q, want %q", got, wantTail)
	}
}

// TestRawSections 验证注释、CDATA、PI 内的 & 不被当作实体引用。
func TestRawSections(t *testing.T) {
	input := `<?xml version="1.0"?><!-- &notexpanded; --><!DOCTYPE r [<!ENTITY e "X">]>` +
		`<r><![CDATA[ &e; raw ]]><?pi &e; ?>&e;</r>`
	got, err := runReader(t, input, Limits{}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "<!-- &notexpanded; -->") {
		t.Fatalf("comment not preserved: %q", s)
	}
	if !strings.Contains(s, "<![CDATA[ &e; raw ]]>") {
		t.Fatalf("CDATA not preserved: %q", s)
	}
	if !strings.Contains(s, "<?pi &e; ?>") {
		t.Fatalf("PI not preserved: %q", s)
	}
	if !strings.HasSuffix(s, "X</r>") {
		t.Fatalf("entity outside raw sections not expanded: %q", s)
	}
}

// TestEncodingXMLIntegration 验证展开后的流可被 encoding/xml 正常解码，
// 且外部实体场景在读取器层就被拦住，xml 解码器根本看不到危险内容。
func TestEncodingXMLIntegration(t *testing.T) {
	input := `<!DOCTYPE doc [
		<!ENTITY name "Pair-wise GSB">
		<!ENTITY greeting "hello &name;">
	]><doc><greet>&greeting;</greet></doc>`

	r := NewReader(strings.NewReader(input), Limits{})
	dec := xml.NewDecoder(r)
	type doc struct {
		Greet string `xml:"greet"`
	}
	var got doc
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if got.Greet != "hello Pair-wise GSB" {
		t.Fatalf("got %q", got.Greet)
	}

	evil := `<!DOCTYPE doc [<!ENTITY x SYSTEM "file:///etc/passwd">]><doc>&x;</doc>`
	_, err := readAll(t, NewReader(strings.NewReader(evil), Limits{}), 32)
	if !errors.Is(err, ErrExternalEntity) {
		t.Fatalf("got %v, want ErrExternalEntity", err)
	}
}

// TestMalformedDTD 验证畸形 DTD 返回 ErrMalformed。
func TestMalformedDTD(t *testing.T) {
	cases := []string{
		`<!DOCTYPE r [<!ENTITY a "x"><r>&a;</r>`,
		`<!DOCTYPE r [<!ENTITY a >]><r/>`,
		`<!DOCTYPE r [<!BOGUS x>]><r/>`,
	}
	for _, input := range cases {
		_, err := runReader(t, input, Limits{}, 8)
		if !errors.Is(err, ErrMalformed) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("input %q: got %v, want ErrMalformed", input, err)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
