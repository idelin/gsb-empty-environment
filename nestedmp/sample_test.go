package nestedmp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"
)

const (
	rootB  = "AB"
	innerB = "ABC" // 与根边界互为前缀
	deepB  = "ABCD"
)

type builtSample struct {
	msg  []byte
	want map[string][]byte
}

// buildSample 构造三层嵌套、前缀边界与各种正文形态的样本报文。
func buildSample(t *testing.T) builtSample {
	t.Helper()
	leafEmpty := []byte{}
	leafCRLF := []byte("line1\r\nline2\r\n")
	leafPartial := []byte("frag --AB\r\nx--ABC\r\ny--ABCD not a real line\r\nz")
	deepBody := []byte("deep-payload")

	var deep wireBuilder
	deep.open(deepB)
	deep.header(TypeAttachment, "deep", digestOf(deepBody), "")
	deep.body(deepBody)
	deep.close(deepB)

	var inner wireBuilder
	inner.open(innerB)
	inner.header(TypeAttachment, "empty", digestOf(leafEmpty), "")
	inner.body(leafEmpty)
	inner.open(innerB)
	inner.header(TypeAttachment, "crlf", digestOf(leafCRLF), "")
	inner.body(leafCRLF)
	inner.open(innerB)
	inner.header(TypeAttachment, "partial", digestOf(leafPartial), "")
	inner.body(leafPartial)
	inner.open(innerB)
	inner.header(TypeContainer, "deepbox", digestOf(deep.bytes()), deepB)
	inner.body(deep.bytes())
	inner.close(innerB)

	first := []byte("root-first-payload")
	last := []byte("root-last")
	var root wireBuilder
	root.open(rootB)
	root.header(TypeAttachment, "first", digestOf(first), "")
	root.body(first)
	root.open(rootB)
	root.header(TypeContainer, "box", digestOf(inner.bytes()), innerB)
	root.body(inner.bytes())
	root.open(rootB)
	root.header(TypeAttachment, "last", digestOf(last), "")
	root.body(last)
	root.close(rootB)

	return builtSample{
		msg: root.bytes(),
		want: map[string][]byte{
			"first":   first,
			"empty":   leafEmpty,
			"crlf":    leafCRLF,
			"partial": leafPartial,
			"deep":    deepBody,
			"last":    last,
		},
	}
}

func runStream(t *testing.T, chunks [][]byte, opts Options) (*Result, *fakeHost, *recorder, error) {
	t.Helper()
	host := &fakeHost{}
	rec := &recorder{}
	opts.Host = host
	opts.RootBoundary = rootB
	opts.Tracer = rec
	p, err := New(context.Background(), opts)
	if err != nil {
		return nil, host, rec, err
	}
	for _, ch := range chunks {
		if _, err := p.WriteCtx(context.Background(), ch); err != nil {
			return nil, host, rec, err
		}
	}
	res, err := p.CloseCtx(context.Background())
	return res, host, rec, err
}

func flattenRef(parts []RefPart) []RefPart {
	var out []RefPart
	var walk func([]RefPart)
	walk = func(ps []RefPart) {
		for _, p := range ps {
			out = append(out, p)
			walk(p.Children)
		}
	}
	walk(parts)
	return out
}

var (
	_ = sha256.Size
	_ = bytes.Equal
)
