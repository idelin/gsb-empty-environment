package xmlentity

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty entity",
			input: `<!DOCTYPE r [<!ENTITY e "">]><r>a&e;b</r>`,
			want:  `<!DOCTYPE r [<!ENTITY e "">]><r>ab</r>`,
		},
		{
			name:  "entity containing markup",
			input: `<!DOCTYPE r [<!ENTITY e "<b>x</b>">]><r>&e;</r>`,
			want:  `<!DOCTYPE r [<!ENTITY e "<b>x</b>">]><r><b>x</b></r>`,
		},
		{
			name:  "nested predefined inside entity",
			input: `<!DOCTYPE r [<!ENTITY e "a&amp;b">]><r>&e;</r>`,
			want:  `<!DOCTYPE r [<!ENTITY e "a&amp;b">]><r>a&amp;b</r>`,
		},
		{
			name:  "utf8 content",
			input: `<!DOCTYPE r [<!ENTITY e "中文">]><r>&e;!</r>`,
			want:  `<!DOCTYPE r [<!ENTITY e "中文">]><r>中文!</r>`,
		},
		{
			name:  "bare amp at eof",
			input: `<r>x&`,
			want:  `<r>x&`,
		},
		{
			name:  "dangling name at eof",
			input: `<!DOCTYPE r [<!ENTITY ab "X">]><r>&ab`,
			want:  `<!DOCTYPE r [<!ENTITY ab "X">]><r>&ab`,
		},
		{
			name:  "comment with doctype-like text",
			input: `<!-- <!DOCTYPE fake --><r>&a;</r>`,
			want:  `<!-- <!DOCTYPE fake --><r>&a;</r>`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, bs := range []int{1, 2, 5, 128} {
				got, err := runReader(t, tc.input, Limits{}, bs)
				if err != nil {
					t.Fatalf("bufSize=%d: %v", bs, err)
				}
				if string(got) != tc.want {
					t.Fatalf("bufSize=%d:\n got %q\nwant %q", bs, got, tc.want)
				}
			}
		})
	}
}

func TestReadEmpty(t *testing.T) {
	r := NewReader(strings.NewReader("<r/>"), Limits{})
	n, err := r.Read(make([]byte, 0))
	if n != 0 || err != nil {
		t.Fatalf("empty buffer: n=%d err=%v", n, err)
	}
}

func TestStreamingNoFullBuffer(t *testing.T) {
	// 大文档：验证读取器内部缓冲不随文档增长（outPending/dtdPending 有界）。
	var input strings.Builder
	input.WriteString(`<!DOCTYPE r [<!ENTITY w "abc">]><r>`)
	for i := 0; i < 50_000; i++ {
		input.WriteString("&w;")
	}
	input.WriteString("</r>")
	r := NewReader(strings.NewReader(input.String()), Limits{
		MaxExpandedBytes: 32 << 20, MaxRefsPerEntity: 60_000, MaxTotalEntityRefs: 60_000,
	})
	buf := make([]byte, 37)
	var total int
	for {
		n, err := r.Read(buf)
		total += n
		if cap(r.outPending) > 1<<16 || cap(r.dtdPending) > 1<<16 {
			t.Fatalf("internal buffer grew unbounded: outCap=%d dtdCap=%d",
				cap(r.outPending), cap(r.dtdPending))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if total == 0 {
		t.Fatal("no output")
	}
}

// TestLargeEntityValueBounded 验证单个大实体值受 MaxDTDBytes 约束，
// 且实体定义存储只占一份（多次引用共享，不复制）。
func TestLargeEntityValueBounded(t *testing.T) {
	bigValue := strings.Repeat("z", 50_000)
	input := `<!DOCTYPE r [<!ENTITY big "` + bigValue + `">]><r>&big;&big;</r>`

	// DTD 超过 4096 字节预算时应被拒绝。
	_, err := runReader(t, input, Limits{MaxDTDBytes: 4096}, 64)
	var le *LimitError
	if !errors.As(err, &le) || le.Limit != LimitDTDBytes {
		t.Fatalf("got %v, want LimitDTDBytes", err)
	}

	// 放宽 DTD 预算后，两次引用共享同一份 value，输出是两倍长度但内存平稳。
	out, err := runReader(t, input, Limits{MaxDTDBytes: 1 << 16, MaxExpandedBytes: 1 << 18}, 64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantTail := "<r>" + bigValue + bigValue + "</r>"
	if !strings.HasSuffix(string(out), wantTail) {
		t.Fatalf("large entity expansion mismatch (len got=%d want=%d)",
			len(out), len(input)-len(bigValue)+2*len(bigValue))
	}

	r := NewReader(strings.NewReader(input), Limits{MaxDTDBytes: 1 << 16, MaxExpandedBytes: 1 << 18})
	_, _ = io.Copy(io.Discard, r)
	if len(r.entities) != 1 {
		t.Fatalf("entities = %d, want 1", len(r.entities))
	}
	if len(r.entities["big"].value) != len(bigValue) {
		t.Fatalf("stored value len = %d, want %d", len(r.entities["big"].value), len(bigValue))
	}
}

// TestQuadraticAttackStopped 验证二次方膨胀（实体被高频引用）同样被预算拦截。
func TestQuadraticAttackStopped(t *testing.T) {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE r [<!ENTITY a "01234567890123456789012345678901">]><r>`)
	for i := 0; i < 100_000; i++ {
		sb.WriteString("&a;")
	}
	sb.WriteString("</r>")
	_, err := runReader(t, sb.String(), Limits{MaxRefsPerEntity: 1000, MaxExpandedBytes: 1 << 20}, 32)
	var le *LimitError
	if !errors.As(err, &le) {
		t.Fatalf("got %v, want LimitError", err)
	}
	if le.Limit != LimitRefsPerEntity && le.Limit != LimitExpandedBytes {
		t.Fatalf("unexpected limit kind: %s", le.Limit)
	}
}

// TestSiblingAndCrossLevelEntities 验证兄弟实体、跨层级同名引用与
// 非递归的重复嵌套都能正确展开。
func TestSiblingAndCrossLevelEntities(t *testing.T) {
	input := `<!DOCTYPE r [
		<!ENTITY a "A">
		<!ENTITY b "B&a;B">
		<!ENTITY c "&b;&b;">
	]><r>&a;| &b;| &c;| &a;</r>`
	want := `<!DOCTYPE r [
		<!ENTITY a "A">
		<!ENTITY b "B&a;B">
		<!ENTITY c "&b;&b;">
	]><r>A| BAB| BABBAB| A</r>`
	for _, bs := range []int{1, 7, 4096} {
		got, err := runReader(t, input, Limits{}, bs)
		if err != nil {
			t.Fatalf("bufSize=%d: %v", bs, err)
		}
		if string(got) != want {
			t.Fatalf("bufSize=%d:\n got %q\nwant %q", bs, got, want)
		}
	}
}

// TestEntityInAttribute 验证属性值中的实体引用同样被展开。
func TestEntityInAttribute(t *testing.T) {
	input := `<!DOCTYPE r [<!ENTITY v "123">]><r id="x-&v;-y" k='&v;'/>`
	got, err := runReader(t, input, Limits{}, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `<!DOCTYPE r [<!ENTITY v "123">]><r id="x-123-y" k='123'/>`
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
