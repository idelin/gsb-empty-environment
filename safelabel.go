// Package safelabel 把不可信的文件名渲染为可安全嵌入 HTML 页面的片段。
//
// 渲染结果满足四个安全目标：
//   - 所有 C0/C1 控制符、双向控制符与不可见格式符都替换为 ⟨TAG⟩ 形式的
//     可见记号，双向覆盖符（如 U+202E RLO）无法再隐藏真实扩展名；
//   - 整体输出包裹在 dir="auto" 且 unicode-bidi:isolate 的元素中，
//     文件名内部的文本方向不会泄漏到相邻页面文本；
//   - 每个记号与高亮区间都携带 data-b0/data-b1 属性，对应原始字符串的
//     字节偏移（左闭右开），Segments 同时返回程序化可用的字节范围；
//   - 所有来源于输入的字符都经过 HTML 转义，输出中唯一的标签来自本包
//     自身的固定模板，杜绝 HTML 注入。
package safelabel

import (
	"fmt"
	"html"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind 描述 Segment 的类型。
type Kind int

const (
	// KindText 是普通可见文本片段，内容已做 HTML 转义。
	KindText Kind = iota
	// KindControl 是被可视化的控制符或不可见格式符。
	KindControl
	// KindInvalid 是输入中的非法 UTF-8 字节，逐字节保留范围。
	KindInvalid
)

// Segment 是文件名的一个渲染片段，Start/End 为原始字符串的字节偏移，
// 左闭右开，且 name[Start:End] == Text。
type Segment struct {
	Start int
	End   int
	Kind  Kind
	// Text 是该片段对应的原始子串，未做任何转义。
	Text string
	// HTML 是该片段对应的安全 HTML，可直接拼接输出。
	HTML template.HTML
}

// Range 是原始文件名上的左闭右开字节区间 [Start, End)，用于高亮。
type Range [2]int

const (
	isolateOpen  = `<span class="safelabel" dir="auto" style="unicode-bidi:isolate">`
	isolateClose = `</span>`
)

// Stylesheet 是可选的样式表，配合 Render 输出的 class 使用。
const Stylesheet template.CSS = template.CSS(`
.safelabel{unicode-bidi:isolate}
.sl-ctrl,.sl-bad{
	background:#fde2e2;
	border:1px solid #e0a0a0;
	border-radius:3px;
	font-family:ui-monospace,SFMono-Regular,Menlo,monospace;
	font-style:normal;
	white-space:pre;
}
.sl-hl{background:#ffe08a;border-radius:2px;}
`)

// c0Abbr 是 ASCII C0 控制符的标准缩写。
var c0Abbr = [32]string{
	"NUL", "SOH", "STX", "ETX", "EOT", "ENQ", "ACK", "BEL",
	"BS", "TAB", "LF", "VT", "FF", "CR", "SO", "SI",
	"DLE", "DC1", "DC2", "DC3", "DC4", "NAK", "SYN", "ETB",
	"CAN", "EM", "SUB", "ESC", "FS", "GS", "RS", "US",
}

// formatInfo 记录常见不可见格式符（均为 Unicode Cf 类别）的缩写与全称，
// 其中包含全部双向嵌入/覆盖/隔离控制符。
var formatInfo = map[rune][2]string{
	0x00AD: {"SHY", "SOFT HYPHEN"},
	0x200E: {"LRM", "LEFT-TO-RIGHT MARK"},
	0x200F: {"RLM", "RIGHT-TO-LEFT MARK"},
	0x2028: {"LSEP", "LINE SEPARATOR"},
	0x2029: {"PSEP", "PARAGRAPH SEPARATOR"},
	0x202A: {"LRE", "LEFT-TO-RIGHT EMBEDDING"},
	0x202B: {"RLE", "RIGHT-TO-LEFT EMBEDDING"},
	0x202C: {"PDF", "POP DIRECTIONAL FORMATTING"},
	0x202D: {"LRO", "LEFT-TO-RIGHT OVERRIDE"},
	0x202E: {"RLO", "RIGHT-TO-LEFT OVERRIDE"},
	0x2060: {"WJ", "WORD JOINER"},
	0x2066: {"LRI", "LEFT-TO-RIGHT ISOLATE"},
	0x2067: {"RLI", "RIGHT-TO-LEFT ISOLATE"},
	0x2068: {"FSI", "FIRST STRONG ISOLATE"},
	0x2069: {"PDI", "POP DIRECTIONAL ISOLATE"},
	0x200B: {"ZWSP", "ZERO WIDTH SPACE"},
	0x200C: {"ZWNJ", "ZERO WIDTH NON-JOINER"},
	0x200D: {"ZWJ", "ZERO WIDTH JOINER"},
	0xFEFF: {"ZWNBSP", "ZERO WIDTH NO-BREAK SPACE"},
}

// controlInfo 判断 r 是否为需要可视化的控制符或格式符，
// 返回记号缩写与 title 提示文本。
func controlInfo(r rune) (label, title string, ok bool) {
	switch {
	case r >= 0x00 && r < 0x20:
		return c0Abbr[r], fmt.Sprintf("U+%04X CONTROL", r), true
	case r == 0x7F:
		return "DEL", "U+007F CONTROL", true
	case r >= 0x80 && r <= 0x9F:
		return fmt.Sprintf("U+%04X", r), fmt.Sprintf("U+%04X CONTROL", r), true
	}
	if info, found := formatInfo[r]; found {
		return info[0], fmt.Sprintf("U+%04X %s", r, info[1]), true
	}
	// 兜底：所有其他 Cf 格式符（如 U+E0001 语言标签符）一律可见化，
	// 避免遗漏未制表的不可见字符。
	if unicode.Is(unicode.Cf, r) {
		return fmt.Sprintf("U+%04X", r), fmt.Sprintf("U+%04X FORMAT", r), true
	}
	return "", "", false
}

// marker 生成控制符/非法字节的可见记号标签。所有动态部分均经过转义。
func marker(class string, start, end int, label, title string) template.HTML {
	var b strings.Builder
	b.WriteString(`<span class="`)
	b.WriteString(class)
	b.WriteString(`" data-b0="`)
	b.WriteString(strconv.Itoa(start))
	b.WriteString(`" data-b1="`)
	b.WriteString(strconv.Itoa(end))
	b.WriteString(`" title="`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`">⟨`)
	b.WriteString(html.EscapeString(label))
	b.WriteString(`⟩</span>`)
	return template.HTML(b.String())
}

// Segments 把文件名切分为带原始字节范围的渲染片段。
// 返回的片段连续且完整覆盖 [0, len(name))，可用于按字节范围高亮。
func Segments(name string) []Segment {
	segs := make([]Segment, 0)
	textStart := 0

	flushText := func(end int) {
		if textStart >= end {
			return
		}
		sub := name[textStart:end]
		segs = append(segs, Segment{
			Start: textStart,
			End:   end,
			Kind:  KindText,
			Text:  sub,
			HTML:  template.HTML(html.EscapeString(sub)),
		})
	}

	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])

		if r == utf8.RuneError && size == 1 {
			flushText(i)
			label := fmt.Sprintf("BAD-UTF8:0x%02X", name[i])
			title := fmt.Sprintf("INVALID UTF-8 BYTE 0x%02X", name[i])
			segs = append(segs, Segment{
				Start: i,
				End:   i + 1,
				Kind:  KindInvalid,
				Text:  name[i : i+1],
				HTML:  marker("sl-bad", i, i+1, label, title),
			})
			i++
			textStart = i
			continue
		}

		if label, title, found := controlInfo(r); found {
			flushText(i)
			segs = append(segs, Segment{
				Start: i,
				End:   i + size,
				Kind:  KindControl,
				Text:  name[i : i+size],
				HTML:  marker("sl-ctrl", i, i+size, label, title),
			})
			i += size
			textStart = i
			continue
		}

		i += size
	}
	flushText(len(name))
	return segs
}

// Render 把文件名渲染为带双向隔离的安全 HTML 片段。
// 返回 template.HTML 是因为其中的标签全部由本包生成且输入已转义，
// 在 html/template 中使用时不会被二次转义。
func Render(name string) template.HTML {
	var b strings.Builder
	b.Grow(len(name) + len(isolateOpen) + len(isolateClose))
	b.WriteString(isolateOpen)
	for _, seg := range Segments(name) {
		b.WriteString(string(seg.HTML))
	}
	b.WriteString(isolateClose)
	return template.HTML(b.String())
}

// normalizeRanges 过滤非法区间、排序并合并重叠区间。
func normalizeRanges(ranges []Range, n int) []Range {
	clean := make([]Range, 0, len(ranges))
	for _, rr := range ranges {
		if rr[0] < 0 || rr[1] > n || rr[0] >= rr[1] {
			continue
		}
		clean = append(clean, rr)
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i][0] < clean[j][0] })

	merged := make([]Range, 0, len(clean))
	for _, rr := range clean {
		if last := len(merged) - 1; last >= 0 && rr[0] <= merged[last][1] {
			if rr[1] > merged[last][1] {
				merged[last][1] = rr[1]
			}
			continue
		}
		merged = append(merged, rr)
	}
	return merged
}

// snapBoundary 把字节偏移向后对齐到 rune 起始位置，避免截断多字节字符。
func snapBoundary(name string, pos, end int) int {
	for pos < end && !utf8.RuneStart(name[pos]) {
		pos++
	}
	return pos
}

func writeMark(b *strings.Builder, raw string, start, end int) {
	b.WriteString(`<mark class="sl-hl" data-b0="`)
	b.WriteString(strconv.Itoa(start))
	b.WriteString(`" data-b1="`)
	b.WriteString(strconv.Itoa(end))
	b.WriteString(`">`)
	b.WriteString(html.EscapeString(raw))
	b.WriteString(`</mark>`)
}

// emitText 输出文本片段，按高亮区间切开并包裹 <mark>。
func emitText(name string, seg Segment, ranges []Range, ri int, b *strings.Builder) int {
	pos := seg.Start
	for pos < seg.End {
		for ri < len(ranges) && ranges[ri][1] <= pos {
			ri++
		}
		if ri == len(ranges) || ranges[ri][0] >= seg.End {
			b.WriteString(html.EscapeString(name[pos:seg.End]))
			return ri
		}

		rr := ranges[ri]
		cut := rr[0]
		if cut < pos {
			cut = pos
		}
		cut = snapBoundary(name, cut, seg.End)
		if cut > pos {
			b.WriteString(html.EscapeString(name[pos:cut]))
			pos = cut
			if pos >= seg.End {
				return ri
			}
		}

		coverEnd := rr[1]
		if coverEnd > seg.End {
			coverEnd = seg.End
		}
		coverEnd = snapBoundary(name, coverEnd, seg.End)
		if coverEnd <= pos {
			// 区间落在某个多字节字符内部，跳过该区间对本片段的覆盖。
			ri++
			continue
		}
		writeMark(b, name[pos:coverEnd], pos, coverEnd)
		pos = coverEnd
	}
	return ri
}

// emitAtomic 输出控制符/非法字节片段；这类片段只在被完整覆盖时高亮。
func emitAtomic(seg Segment, ranges []Range, ri int, b *strings.Builder) int {
	for ri < len(ranges) && ranges[ri][1] <= seg.Start {
		ri++
	}
	if ri < len(ranges) && ranges[ri][0] <= seg.Start && ranges[ri][1] >= seg.End {
		// writeMark 写入开标签后，补回记号本体与闭标签。
		writeMark(b, "", seg.Start, seg.End)
		b.WriteString(string(seg.HTML))
		b.WriteString(`</mark>`)
		return ri
	}
	b.WriteString(string(seg.HTML))
	return ri
}

// RenderHighlight 与 Render 相同，但把 ranges 覆盖的原始字节区间
// 包裹为 <mark class="sl-hl">。越界、空区间会被忽略，重叠区间会合并。
func RenderHighlight(name string, ranges []Range) template.HTML {
	merged := normalizeRanges(ranges, len(name))
	var b strings.Builder
	b.Grow(len(name) + len(isolateOpen) + len(isolateClose))
	b.WriteString(isolateOpen)

	ri := 0
	for _, seg := range Segments(name) {
		if seg.Kind == KindText {
			ri = emitText(name, seg, merged, ri, &b)
		} else {
			ri = emitAtomic(seg, merged, ri, &b)
		}
	}
	b.WriteString(isolateClose)
	return template.HTML(b.String())
}

// FuncMap 返回可直接传入 html/template 的函数表：
//
//	tmpl := template.Must(template.New("").Funcs(safelabel.FuncMap()).Parse(
//	    `{{ safelabel .Name }}`))
//	tmpl := template.Must(template.New("").Funcs(safelabel.FuncMap()).Parse(
//	    `{{ safelabel_hl .Name .Ranges }}`))
func FuncMap() template.FuncMap {
	return template.FuncMap{
		"safelabel": func(name string) template.HTML {
			return Render(name)
		},
		"safelabel_hl": func(name string, ranges []Range) template.HTML {
			return RenderHighlight(name, ranges)
		},
	}
}
