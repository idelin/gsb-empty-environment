package safelabel_test

import (
	"html/template"
	"strings"
	"testing"
	"unicode/utf8"

	"example.org/safelabel"
)

// 本包输出中允许出现的标签名。
var allowedTagNames = map[string]bool{"span": true, "mark": true}

func assertOnlyPackageTags(t *testing.T, out string) {
	t.Helper()
	i := 0
	for {
		open := strings.IndexByte(out[i:], '<')
		if open < 0 {
			return
		}
		open += i
		close := strings.IndexByte(out[open:], '>')
		if close < 0 {
			t.Fatalf("unclosed tag: %q", out)
		}
		close += open
		tag := strings.Fields(strings.TrimPrefix(out[open+1:close], "/"))[0]
		if !allowedTagNames[tag] {
			t.Fatalf("unexpected tag %q in %q", tag, out)
		}
		i = close + 1
	}
}

func FuzzRenderSafety(f *testing.F) {
	f.Add("budget_2026\u202etxt.exe", []byte{0, 4, 4, 7})
	f.Add("<img src=x onerror=alert(1)>", []byte{0, 3})
	f.Add("cafe\u0301\xff.txt", []byte{})

	f.Fuzz(func(t *testing.T, name string, rangesBytes []byte) {
		out := string(safelabel.Render(name))

		// 输出恒为合法 UTF-8：非法输入字节只能以 ASCII 记号呈现。
		if !utf8.ValidString(out) {
			t.Fatalf("output is not valid UTF-8: %q", out)
		}
		// 输入中的控制/格式 rune 不允许原样泄漏（记号里只有 ASCII 缩写）。
		for _, r := range name {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				if strings.ContainsRune(out, r) {
					t.Fatalf("control U+%04X leaked into %q", r, out)
				}
			}
		}
		for _, r := range []rune{0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
			0x2066, 0x2067, 0x2068, 0x2069, 0x200B, 0x200C, 0x200D, 0xFEFF} {
			if strings.ContainsRune(name, r) && strings.ContainsRune(out, r) {
				t.Fatalf("bidi/format rune U+%04X leaked into %q", r, out)
			}
		}
		assertOnlyPackageTags(t, out)

		// 片段必须连续完整覆盖原始字节。
		segs := safelabel.Segments(name)
		pos := 0
		for _, seg := range segs {
			if seg.Start != pos || name[seg.Start:seg.End] != seg.Text {
				t.Fatalf("segment coverage broken: %+v for %q", seg, name)
			}
			pos = seg.End
		}
		if pos != len(name) {
			t.Fatalf("segments cover %d of %d bytes", pos, len(name))
		}

		// 由 fuzz 字节派生任意（可能畸形的）高亮区间。
		n := len(name)
		ranges := make([]safelabel.Range, 0, len(rangesBytes)/2)
		for k := 0; k+1 < len(rangesBytes); k += 2 {
			ranges = append(ranges, safelabel.Range{
				int(rangesBytes[k]) % (n + 2),
				int(rangesBytes[k+1]) % (n + 2),
			})
		}
		hlOut := string(safelabel.RenderHighlight(name, ranges))
		if !utf8.ValidString(hlOut) {
			t.Fatalf("highlight output is not valid UTF-8: %q", hlOut)
		}
		assertOnlyPackageTags(t, hlOut)

		// 经 html/template 渲染后类型保持安全 HTML，不被二次转义。
		tmpl := template.Must(template.New("f").Funcs(safelabel.FuncMap()).
			Parse(`{{ safelabel .Name }}`))
		var sb strings.Builder
		if err := tmpl.Execute(&sb, map[string]any{"Name": name}); err != nil {
			t.Fatalf("template execute: %v", err)
		}
		if sb.String() != out {
			t.Fatalf("template output mismatch:\nwant %q\n got %q", out, sb.String())
		}
	})
}
