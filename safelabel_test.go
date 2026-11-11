package safelabel_test

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"example.org/safelabel"
)

// renderText 用 html/template 执行模板，确保走真实模板管线。
func renderText(t *testing.T, tmplText string, data any) string {
	t.Helper()
	tmpl := template.Must(template.New("t").Funcs(safelabel.FuncMap()).Parse(tmplText))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template execute: %v", err)
	}
	return buf.String()
}

// assertNoForeignMarkup 校验输出中除本包固定标签外不存在任何输入来源的标签。
func assertNoForeignMarkup(t *testing.T, out string, allowed []string) {
	t.Helper()
	allowedTags := map[string]bool{
		"span": true, "mark": true,
	}
	for _, tag := range allowed {
		allowedTags[tag] = true
	}
	i := 0
	for {
		open := strings.IndexByte(out[i:], '<')
		if open < 0 {
			return
		}
		open += i
		close := strings.IndexByte(out[open:], '>')
		if close < 0 {
			t.Fatalf("unclosed tag in output: %q", out)
		}
		close += open
		tagText := out[open+1 : close]
		tagText = strings.TrimPrefix(tagText, "/")
		tagText = strings.TrimSpace(strings.Fields(tagText)[0])
		if !allowedTags[tagText] {
			t.Fatalf("foreign/unescaped markup <%s> in output: %q", tagText, out)
		}
		i = close + 1
	}
}

// TestSegmentsCoverInput 是所有用例共享的不变量：
// 片段连续覆盖全部原始字节，且 Text 与原始子串完全一致。
func TestSegmentsCoverInput(t *testing.T) {
	cases := []string{
		"",
		"plain.txt",
		"e\x03\u202efinal.exe",
		"caf\u0065\u0301",
		"bad\xff\xfeutf8\x80",
		"<script>alert(1)</script>",
		"\u0627\u0644\u0645\u0644\u0641 123 \u0661\u0662\u0663.txt",
		"a\u200db\u200dc",
	}
	for _, name := range cases {
		segs := safelabel.Segments(name)
		if len(segs) == 0 && len(name) != 0 {
			t.Fatalf("no segments for %q", name)
		}
		pos := 0
		for _, seg := range segs {
			if seg.Start != pos {
				t.Fatalf("gap/overlap: want start %d got %d for %q", pos, seg.Start, name)
			}
			if seg.End <= seg.Start {
				t.Fatalf("empty segment in %q", name)
			}
			if name[seg.Start:seg.End] != seg.Text {
				t.Fatalf("segment text mismatch: %q != %q", seg.Text, name[seg.Start:seg.End])
			}
			pos = seg.End
		}
		if pos != len(name) {
			t.Fatalf("segments cover %d of %d bytes for %q", pos, len(name), name)
		}
	}
}

// TestCombiningGrapheme 组合字素保持在同一可见文本片段，不会被拆开。
func TestCombiningGrapheme(t *testing.T) {
	name := "cafe\u0301-zalgo\u0308\u0323\u0301.txt"
	out := string(safelabel.Render(name))

	// 组合附加符不是 HTML 特殊字符，原样保留且与基字符同段，不被拆成记号。
	if !strings.Contains(out, "cafe\u0301-zalgo") {
		t.Fatalf("combining sequence should remain intact: %q", out)
	}
	segs := safelabel.Segments(name)
	if len(segs) != 1 || segs[0].Kind != safelabel.KindText {
		t.Fatalf("combining grapheme should be one text segment, got %+v", segs)
	}

	// ZWJ 属于不可见格式符，必须可见化，不能再参与字形连接。
	zwj := "a\u200db"
	zwjOut := string(safelabel.Render(zwj))
	if strings.ContainsRune(zwjOut, '\u200d') {
		t.Fatalf("ZWJ must not pass through verbatim: %q", zwjOut)
	}
	if !strings.Contains(zwjOut, "⟨ZWJ⟩") {
		t.Fatalf("ZWJ marker missing: %q", zwjOut)
	}
}

// TestInvalidUTF8 非法 UTF-8 逐字节可见化并保留独立字节范围。
func TestInvalidUTF8(t *testing.T) {
	name := "a\xffb\x80c\xfe"
	segs := safelabel.Segments(name)

	bad := 0
	for _, seg := range segs {
		if seg.Kind == safelabel.KindInvalid {
			bad++
			if seg.End-seg.Start != 1 {
				t.Fatalf("invalid byte segment must cover exactly one byte: %+v", seg)
			}
			if name[seg.Start] < 0x80 {
				t.Fatalf("ASCII byte misclassified as invalid: %+v", seg)
			}
		}
	}
	if bad != 3 {
		t.Fatalf("want 3 invalid-byte segments, got %d: %+v", bad, segs)
	}

	out := string(safelabel.Render(name))
	if !strings.Contains(out, "⟨BAD-UTF8:0xFF⟩") ||
		!strings.Contains(out, "⟨BAD-UTF8:0x80⟩") ||
		!strings.Contains(out, "⟨BAD-UTF8:0xFE⟩") {
		t.Fatalf("invalid byte markers missing: %q", out)
	}
	if strings.Contains(out, "\xff") || strings.Contains(out, "\x80") || strings.Contains(out, "\xfe") {
		t.Fatalf("raw invalid bytes leaked into output: %q", out)
	}

	// 合法编码的 C1 控制符（U+0085 NEL）也必须可见化。
	nel := string(safelabel.Render("x\u0085y"))
	if !strings.Contains(nel, "⟨U+0085⟩") {
		t.Fatalf("encoded C1 control not visualized: %q", nel)
	}
}

// TestMixedNumeralsAndBidi 混合数字与双向覆盖符：
// RLO 被呈为可见记号，真实扩展名不再被视觉反转隐藏。
func TestMixedNumeralsAndBidi(t *testing.T) {
	// 经典攻击：RLO 让 "txt.exe" 视觉上像 "txt.exe" 反转的安全名称。
	name := "budget_2026\u202etxt.exe"
	out := string(safelabel.Render(name))

	if strings.ContainsRune(out, '\u202e') {
		t.Fatalf("RLO control char leaked into HTML: %q", out)
	}
	if !strings.Contains(out, "⟨RLO⟩") || !strings.Contains(out, "RIGHT-TO-LEFT OVERRIDE") {
		t.Fatalf("RLO not rendered as visible marker: %q", out)
	}
	if !strings.Contains(out, `data-b0="11" data-b1="14"`) {
		t.Fatalf("RLO marker missing original byte range: %q", out)
	}
	// 真实扩展名字节按原始逻辑顺序保留，没有被 RLO 反转。
	if !strings.Contains(out, "budget_2026") ||
		!strings.Contains(out, "txt.exe</span>") {
		t.Fatalf("real extension bytes must remain in logical order: %q", out)
	}

	// 阿拉伯-印度数字与 ASCII 数字混排：数字保留，方向由外层隔离容器约束。
	mixed := "report 123 \u0661\u0662\u0663 \u06f4\u06f5"
	mixedOut := string(safelabel.Render(mixed))
	for _, want := range []string{"123", "١٢٣", "۴۵"} {
		if !strings.Contains(mixedOut, want) {
			t.Fatalf("mixed numeral %q missing: %q", want, mixedOut)
		}
	}
	if !strings.Contains(mixedOut, `dir="auto"`) ||
		!strings.Contains(mixedOut, "unicode-bidi:isolate") {
		t.Fatalf("bidi isolation wrapper missing: %q", mixedOut)
	}

	// 所有双向控制符都不得原样出现在输出中。
	for _, r := range []rune{0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2066, 0x2067, 0x2068, 0x2069, 0x200E, 0x200F} {
		if strings.ContainsRune(out, r) || strings.ContainsRune(mixedOut, r) {
			t.Fatalf("bidi control U+%04X leaked", r)
		}
	}
}

// TestMaliciousMarkupViaTemplate 恶意标签/属性经模板渲染后只能是转义文本。
func TestMaliciousMarkupViaTemplate(t *testing.T) {
	payloads := []string{
		"<script>alert(1)</script>",
		`<img src=x onerror="alert(1)">.png`,
		`"><svg onload=alert(1)>`,
		"a&b<c>d'e\"f",
		"invoice\u0000.pdf<script src=//evil.example/x.js></script>",
	}
	for _, name := range payloads {
		out := renderText(t, `<div>{{ safelabel .Name }}</div>`, map[string]any{"Name": name})

		// 危险片段只能以转义文本出现；未转义的活动标签形态必须不存在。
		for _, banned := range []string{"<script", "<img", "<svg"} {
			if strings.Contains(strings.ToLower(out), banned) {
				t.Fatalf("payload %q produced active markup %q in %q", name, banned, out)
			}
		}
		if strings.Contains(name, "<script>") && !strings.Contains(out, "&lt;script&gt;") {
			t.Fatalf("script tag not escaped: %q", out)
		}
		if strings.Contains(name, "<img") && !strings.Contains(out, "&lt;img") {
			t.Fatalf("img tag not escaped: %q", out)
		}
		assertNoForeignMarkup(t, out, []string{"div"})

		// 模板自动转义不会二次转义本包输出，但普通字段仍被转义。
		plain := renderText(t, `{{ .Name }}`, map[string]any{"Name": name})
		if strings.Contains(plain, "<script>") {
			t.Fatalf("baseline escaping broken for %q", plain)
		}
	}
}

// TestRenderHighlightRanges 高亮保留原始字节范围，合并重叠区间，
// 且高亮内容同样经过 HTML 转义。
func TestRenderHighlightRanges(t *testing.T) {
	name := "a<b>\u202e.exe"
	ranges := []safelabel.Range{
		{9, 3}, // 反向非法区间，忽略
		{0, 2},
		{1, 4}, // 与上一个重叠，合并为 [0,4)
		{4, 7}, // 完整覆盖 RLO 记号（U+202E 占 3 字节）
		{99, 100},
	}
	out := string(safelabel.RenderHighlight(name, ranges))

	if strings.Count(out, "<mark") != 2 {
		t.Fatalf("want 2 merged marks, got: %q", out)
	}
	if !strings.Contains(out, `data-b0="0" data-b1="4"`) {
		t.Fatalf("merged text range missing: %q", out)
	}
	if !strings.Contains(out, `data-b0="4" data-b1="7"`) {
		t.Fatalf("control marker range missing in highlight: %q", out)
	}
	if strings.Contains(out, "<b>") {
		t.Fatalf("highlighted text must still be escaped: %q", out)
	}

	// 未对齐到 rune 边界的区间会向后吸附，不产生截断的非法 UTF-8。
	snapOut := string(safelabel.RenderHighlight("éx", []safelabel.Range{{0, 1}}))
	if strings.Count(snapOut, "<mark") != 1 || !strings.Contains(snapOut, `data-b0="0" data-b1="2"`) {
		t.Fatalf("range should snap to rune boundary: %q", snapOut)
	}

	// 通过模板调用高亮函数。
	tmplOut := renderText(t, `{{ safelabel_hl .Name .Ranges }}`, map[string]any{
		"Name":   "report.txt",
		"Ranges": []safelabel.Range{{0, 6}},
	})
	if !strings.Contains(tmplOut, "<mark") || !strings.Contains(tmplOut, "report") {
		t.Fatalf("template highlight failed: %q", tmplOut)
	}
	assertNoForeignMarkup(t, tmplOut, nil)
}

// TestRenderWrappingAndEdgeCases 验证隔离包裹与空输入等边界情况。
func TestRenderWrappingAndEdgeCases(t *testing.T) {
	empty := string(safelabel.Render(""))
	if empty != `<span class="safelabel" dir="auto" style="unicode-bidi:isolate"></span>` {
		t.Fatalf("empty name wrapper wrong: %q", empty)
	}

	onlyCtrl := string(safelabel.Render("\t\n"))
	if !strings.Contains(onlyCtrl, "⟨TAB⟩") || !strings.Contains(onlyCtrl, "⟨LF⟩") {
		t.Fatalf("control-only render wrong: %q", onlyCtrl)
	}

	htmlish := string(safelabel.Render("<>\"'&"))
	for _, want := range []string{"&lt;", "&gt;", "&#34;", "&#39;", "&amp;"} {
		if !strings.Contains(htmlish, want) {
			t.Fatalf("expected escaped %s in %q", want, htmlish)
		}
	}

	// 与相邻页面文本之间由隔离 span 明确分界。
	page := renderText(t, `before {{ safelabel .Name }} after`,
		map[string]any{"Name": "name\u202e.txt"})
	if !strings.HasPrefix(page, "before <span") || !strings.HasSuffix(page, "</span> after") {
		t.Fatalf("isolation boundary wrong: %q", page)
	}
}
