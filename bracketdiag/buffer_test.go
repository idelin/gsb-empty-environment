package bracketdiag

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------- 朴素参考实现（独立的全文 O(n) 扫描，用于交叉核对） ----------

// naiveAllErrors 从头扫描全文，返回全部错误事件（按位置升序）。
func naiveAllErrors(text string) []DiagError {
	var errs []DiagError
	var stack []bracketInfo
	state := SNormal
	constructStart := -1

	i := 0
	for i < len(text) {
		c := text[i]
		switch state {
		case SNormal:
			switch {
			case c == '"':
				state, constructStart = SDouble, i
			case c == '\'':
				state, constructStart = SSingle, i
			case c == '`':
				state, constructStart = SBacktick, i
			case c == '/' && i+1 < len(text) && text[i+1] == '/':
				state = SLineComment
				i++
			case c == '/' && i+1 < len(text) && text[i+1] == '*':
				state, constructStart = SBlockComment, i
				i++
			case isOpen(c):
				stack = append(stack, bracketInfo{pos: i, char: c})
			case isClose(c):
				if len(stack) == 0 {
					errs = append(errs, DiagError{Pos: i, Found: string(c)})
				} else if top := stack[len(stack)-1]; closeOf(top.char) == c {
					stack = stack[:len(stack)-1]
				} else {
					errs = append(errs, DiagError{Pos: i, Expected: string(closeOf(top.char)), Found: string(c)})
				}
			}
		case SDouble:
			if c == '\\' {
				state = SEscDouble
			} else if c == '"' {
				state = SNormal
			}
		case SSingle:
			if c == '\\' {
				state = SEscSingle
			} else if c == '\'' {
				state = SNormal
			}
		case SBacktick:
			if c == '\\' {
				state = SEscBacktick
			} else if c == '`' {
				state = SNormal
			}
		case SEscDouble:
			state = SDouble
		case SEscSingle:
			state = SSingle
		case SEscBacktick:
			state = SBacktick
		case SLineComment:
			if c == '\n' {
				state = SNormal
			}
		case SBlockComment:
			if c == '*' && i+1 < len(text) && text[i+1] == '/' {
				state = SNormal
				i++
			}
		}
		i++
	}

	for _, o := range stack { // 未闭合的开括号
		errs = append(errs, DiagError{Pos: o.pos, Expected: string(closeOf(o.char))})
	}
	switch state { // 未终结的字符串/块注释
	case SDouble, SEscDouble:
		errs = append(errs, DiagError{Pos: constructStart, Expected: `"`})
	case SSingle, SEscSingle:
		errs = append(errs, DiagError{Pos: constructStart, Expected: `'`})
	case SBacktick, SEscBacktick:
		errs = append(errs, DiagError{Pos: constructStart, Expected: "`"})
	case SBlockComment:
		errs = append(errs, DiagError{Pos: constructStart, Expected: "*/"})
	}
	sort.SliceStable(errs, func(a, b int) bool { return errs[a].Pos < errs[b].Pos })
	return errs
}

func naiveErrors(text string, start, end int) []DiagError {
	var out []DiagError
	for _, e := range naiveAllErrors(text) {
		if e.Pos >= start && e.Pos < end {
			out = append(out, e)
		}
	}
	return out
}

func equalErrs(a, b []DiagError) bool {
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

// ---------- 定向测试 ----------

func mustErrs(t *testing.T, b *Buffer, start, end int, want ...DiagError) {
	t.Helper()
	got := b.Errors(start, end)
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("Errors(%d,%d) on %q: got %v, want none", start, end, b.Text(), got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Errors(%d,%d) on %q:\n got: %v\nwant: %v", start, end, b.Text(), got, want)
	}
	// FirstError 应与列表首元素一致
	first, ok := b.FirstError(start, end)
	if !ok || first != want[0] {
		t.Fatalf("FirstError(%d,%d) on %q: got %+v (%v), want %+v", start, end, b.Text(), first, ok, want[0])
	}
}

func mustOK(t *testing.T, b *Buffer) {
	t.Helper()
	mustErrs(t, b, 0, len(b.Text()))
}

func TestBasicBrackets(t *testing.T) {
	mustOK(t, NewBuffer("({[]})"))
	mustOK(t, NewBuffer("fn main() { a[i] = (x) }"))
	// 多余闭括号
	mustErrs(t, NewBuffer(")"), 0, 1, DiagError{Pos: 0, Found: ")"})
	// 未闭合开括号 + 不匹配闭括号
	mustErrs(t, NewBuffer("(]"), 0, 2,
		DiagError{Pos: 0, Expected: ")"},
		DiagError{Pos: 1, Expected: ")", Found: "]"})
	// 不匹配后不弹栈，可继续恢复配对：( 最终被 ) 闭合
	mustErrs(t, NewBuffer("(])"), 0, 3,
		DiagError{Pos: 1, Expected: ")", Found: "]"})
	mustErrs(t, NewBuffer("({[}])"), 0, 6,
		DiagError{Pos: 0, Expected: ")"},
		DiagError{Pos: 1, Expected: "}"},
		DiagError{Pos: 3, Expected: "]", Found: "}"},
		DiagError{Pos: 5, Expected: "}", Found: ")"})
}

func TestStringsAndComments(t *testing.T) {
	mustOK(t, NewBuffer(`"(]"`))
	mustOK(t, NewBuffer(`// (]`))
	mustOK(t, NewBuffer("/* (] */"))
	mustOK(t, NewBuffer("/* (\n]\n*/"))
	mustErrs(t, NewBuffer("// (]\n)"), 0, 7, DiagError{Pos: 6, Found: ")"})
	mustErrs(t, NewBuffer("/* (] */ )"), 0, 10, DiagError{Pos: 9, Found: ")"})
	mustErrs(t, NewBuffer(`"abc`), 0, 4, DiagError{Pos: 0, Expected: `"`})
	mustErrs(t, NewBuffer(`/* abc`), 0, 6, DiagError{Pos: 0, Expected: `*/`})
	// 转义：字符串内的 \" 不结束字符串
	mustErrs(t, NewBuffer(`"a\")`), 0, 5, DiagError{Pos: 0, Expected: `"`})
	mustOK(t, NewBuffer(`"a\\"()`))
}

func TestEscapeAcrossBlocks(t *testing.T) {
	// 反斜杠在行尾，转义跨块消费下一行首字符（此处为换行符本身）：
	// `"a\`⏎`")` 中 \ 转义换行，随后 " 正常关闭字符串，) 成为多余闭括号。
	mustErrs(t, NewBuffer("\"a\\\n\")"), 0, 6, DiagError{Pos: 5, Found: ")"})
	// 对照：无换行时 \ 转义的是 "，字符串未终结。
	mustErrs(t, NewBuffer(`"a\")`), 0, 5, DiagError{Pos: 0, Expected: `"`})
	// 转义跨块吃掉换行后，下一行内容仍在字符串内。
	mustOK(t, NewBuffer("\"a\\\nb\""))
}

func TestQuoteChangeFarEffect(t *testing.T) {
	// 50 行 `)` 全部被首尾两个引号包进字符串：无错误。
	var sb strings.Builder
	sb.WriteString("\"\n")
	for i := 0; i < 50; i++ {
		sb.WriteString(")\n")
	}
	sb.WriteString("\"")
	b := NewBuffer(sb.String())
	mustOK(t, b)

	// 删除远处的开引号：所有 `)` 语义翻转，首个错误在第 1 行；
	// 且只需重算被编辑的一行。
	b.Delete(0, 1)
	if b.Recomputed > 2 {
		t.Fatalf("recomputed %d lines for single-line edit, want <= 2", b.Recomputed)
	}
	mustErrs(t, b, 0, 2, DiagError{Pos: 1, Found: ")"})

	// 撤销恢复。
	if !b.Undo() {
		t.Fatal("undo failed")
	}
	mustOK(t, b)
}

func TestRangeQuery(t *testing.T) {
	b := NewBuffer("(] ) (")
	// 全文错误：pos1 期待 ')' 遇到 ']'；pos5 未闭合 '('（pos0 的 '(' 被 pos3 闭合）。
	mustErrs(t, b, 0, 6,
		DiagError{Pos: 1, Expected: ")", Found: "]"},
		DiagError{Pos: 5, Expected: ")"})
	mustErrs(t, b, 1, 4, DiagError{Pos: 1, Expected: ")", Found: "]"})
	mustErrs(t, b, 2, 6, DiagError{Pos: 5, Expected: ")"})
	mustErrs(t, b, 0, 1) // 区间内无错误
	mustErrs(t, b, 2, 4) // 区间内无错误
}

func TestIncrementalRecompute(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line (of) code\n")
	}
	b := NewBuffer(sb.String())

	b.Insert(7*15+3, "x") // 单行内插入
	if b.Recomputed != 1 {
		t.Fatalf("insert within line: recomputed %d, want 1", b.Recomputed)
	}
	b.Insert(7*15+3, "\n") // 拆行
	if b.Recomputed != 2 {
		t.Fatalf("insert newline: recomputed %d, want 2", b.Recomputed)
	}
	b.Delete(7*15+3, 2) // 合并回去
	if b.Recomputed > 2 {
		t.Fatalf("delete across join: recomputed %d, want <= 2", b.Recomputed)
	}
	mustOK(t, b)
}

func TestUndoRestoresText(t *testing.T) {
	b := NewBuffer("hello (world)")
	b.Insert(5, " [")
	b.Delete(0, 3)
	b.Insert(0, "/*")
	for b.Undo() {
	}
	if b.Text() != "hello (world)" {
		t.Fatalf("after undo-all: %q", b.Text())
	}
	mustOK(t, b)
	if b.Undo() {
		t.Fatal("undo on empty stack should return false")
	}
}

// ---------- 随机编辑模糊测试：增量模块 vs 朴素扫描 ----------

func TestRandomEditsAgainstNaive(t *testing.T) {
	for _, seed := range []int64{20261005, 1, 99} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			fuzzRandomEdits(t, seed)
		})
	}
}

func fuzzRandomEdits(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	tokens := []string{
		"(", ")", "[", "]", "{", "}",
		`"`, "'", "`", `\`, "//", "/*", "*/",
		"\n", "a", "bb", " ", "*", "/",
		`\"`, `\\`, "\n\n", "/*/", "`a`",
	}
	buf := NewBuffer("")
	ref := ""
	var refUndo []string

	check := func(iter int) {
		t.Helper()
		if buf.Text() != ref {
			t.Fatalf("iter %d: text mismatch\nbuf: %q\nref: %q", iter, buf.Text(), ref)
		}
		// 全文 + 随机区间的完整错误列表比对
		ranges := [][2]int{{0, len(ref)}}
		for q := 0; q < 4; q++ {
			a, z := rng.Intn(len(ref)+1), rng.Intn(len(ref)+1)
			if a > z {
				a, z = z, a
			}
			ranges = append(ranges, [2]int{a, z})
		}
		for _, r := range ranges {
			want := naiveErrors(ref, r[0], r[1])
			got := buf.Errors(r[0], r[1])
			if !equalErrs(got, want) {
				t.Fatalf("iter %d: range [%d,%d) mismatch on %q\ngot:  %v\nwant: %v",
					iter, r[0], r[1], ref, got, want)
			}
			wantFirst, wantOK := DiagError{}, false
			if len(want) > 0 {
				wantFirst, wantOK = want[0], true
			}
			gotFirst, gotOK := buf.FirstError(r[0], r[1])
			if gotOK != wantOK || gotFirst != wantFirst {
				t.Fatalf("iter %d: FirstError [%d,%d) on %q: got %+v (%v), want %+v (%v)",
					iter, r[0], r[1], ref, gotFirst, gotOK, wantFirst, wantOK)
			}
		}
	}

	for iter := 0; iter < 3000; iter++ {
		switch op := rng.Intn(10); {
		case op < 5 || len(ref) == 0: // 插入
			pos := rng.Intn(len(ref) + 1)
			var sb strings.Builder
			for k := 0; k < 1+rng.Intn(5); k++ {
				sb.WriteString(tokens[rng.Intn(len(tokens))])
			}
			s := sb.String()
			buf.Insert(pos, s)
			refUndo = append(refUndo, ref)
			ref = ref[:pos] + s + ref[pos:]
		case op < 8: // 删除
			pos := rng.Intn(len(ref))
			n := 1 + rng.Intn(min(10, len(ref)-pos))
			buf.Delete(pos, n)
			refUndo = append(refUndo, ref)
			ref = ref[:pos] + ref[pos+n:]
		default: // 撤销
			if buf.Undo() {
				ref = refUndo[len(refUndo)-1]
				refUndo = refUndo[:len(refUndo)-1]
			}
		}
		check(iter)
	}
}

// TestIncrementalConsistency 随机编辑下，周期性将增量维护的
// 行索引与摘要同全新构建对比，确保只重算部分行不会累积偏差。
func TestIncrementalConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	tokens := []string{"(", ")", "[", "]", "{", "}", `"`, `'`, "`", `\`, "//", "/*", "*/", "\n", "x"}
	buf := NewBuffer("")
	ref := ""
	var refUndo []string

	for iter := 0; iter < 2000; iter++ {
		switch op := rng.Intn(10); {
		case op < 5 || len(ref) == 0:
			pos := rng.Intn(len(ref) + 1)
			s := tokens[rng.Intn(len(tokens))]
			buf.Insert(pos, s)
			refUndo = append(refUndo, ref)
			ref = ref[:pos] + s + ref[pos:]
		case op < 8:
			pos := rng.Intn(len(ref))
			n := 1 + rng.Intn(min(6, len(ref)-pos))
			buf.Delete(pos, n)
			refUndo = append(refUndo, ref)
			ref = ref[:pos] + ref[pos+n:]
		default:
			if buf.Undo() {
				ref = refUndo[len(refUndo)-1]
				refUndo = refUndo[:len(refUndo)-1]
			}
		}

		if iter%25 == 0 {
			fresh := NewBuffer(ref)
			if !reflect.DeepEqual(buf.lineStart, fresh.lineStart) {
				t.Fatalf("iter %d: lineStart %v != fresh %v (text %q)", iter, buf.lineStart, fresh.lineStart, ref)
			}
			if !reflect.DeepEqual(buf.sums, fresh.sums) {
				t.Fatalf("iter %d: summaries diverged (text %q)", iter, ref)
			}
		}
	}
}
