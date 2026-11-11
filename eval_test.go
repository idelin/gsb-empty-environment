package jsonpathstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
)

// chunkReader feeds r in fixed-size chunks to prove the engine never relies on
// token boundaries (input arrives in arbitrary blocks).
type chunkReader struct {
	data []byte
	size int
	pos  int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.pos:])
	if c.size > 0 && n > c.size {
		n = c.size
	}
	c.pos += n
	if c.pos >= len(c.data) {
		return n, io.EOF
	}
	return n, nil
}

// referenceEval decodes the whole document and evaluates it in memory.
func referenceEval(t *testing.T, p *Path, doc []byte) []Match {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(doc))
	root, err := DecodeAll(d)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	return EvaluateAll(p, root)
}

func streamEval(t *testing.T, p *Path, doc []byte, chunk int) []Match {
	t.Helper()
	var got []Match
	err := EvaluateReader(p, &chunkReader{data: doc, size: chunk}, func(m Match) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatalf("EvaluateReader (chunk=%d): %v", chunk, err)
	}
	return got
}

func assertMatches(t *testing.T, q string, got, want []Match) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d matches %v, want %d %v", q, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Path != want[i].Path {
			t.Fatalf("%s: path[%d] got %q want %q", q, i, got[i].Path, want[i].Path)
		}
		if !reflect.DeepEqual(got[i].Value, want[i].Value) {
			t.Fatalf("%s: value[%d] got %#v want %#v", q, i, got[i].Value, want[i].Value)
		}
	}
}

// buildDoc builds a deterministic, nested document exercising objects, arrays,
// scalars, nulls, nested recursion and fields used by filters.
func buildDoc() map[string]any {
	book := func(id int, title string, price float64, active bool, tag string) map[string]any {
		return map[string]any{
			"id":     float64(id),
			"title":  title,
			"price":  price,
			"active": active,
			"tag":    tag,
			"meta":   map[string]any{"score": price * 2, "nested": map[string]any{"k": title}},
		}
	}
	return map[string]any{
		"store": map[string]any{
			"name": "shop",
			"book": []any{
				book(1, "Alpha", 10, true, "x"),
				book(2, "Bravo", 5, false, "y"),
				book(3, "Charlie", 15, true, "x"),
				book(4, "Delta", 7, true, "z"),
			},
			"prices": []any{1.0, 2.0, 3.0, 4.0, 5.0},
		},
		"count": float64(4),
		"maybe": nil,
	}
}

func TestParity(t *testing.T) {
	doc, err := json.Marshal(buildDoc())
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{
		"$",
		"$.count",
		"$.maybe",
		"$.store.name",
		"$.store.missing",
		"$.store.book",
		"$.store.book[*]",
		"$.store.book[0].title",
		"$.store.book[2].id",
		"$.store.book[-1].title",
		"$.store.book[0:2].title",
		"$.store.book[:3].id",
		"$.store.book[1::2].title",
		"$.store.book[-2:].title",
		"$.store.prices[*]",
		"$..title",
		"$..book[*].price",
		"$..k",
		"$..*",
		"$.store.book[?(@.price < 8)].title",
		"$.store.book[?(@.active == true)].id",
		"$.store.book[?(@.tag == \"x\")].title",
		"$.store.book[?(@.price >= 10)].meta.score",
		"$..book[?(@.id == 3)].title",
		"$.store.book[?(@.price < @.meta.score)].title",
		"$..[?(@.active == false)].tag",
	}
	chunks := []int{1, 2, 3, 7, 64, 4096}
	for _, q := range queries {
		p, err := Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		want := referenceEval(t, p, doc)
		for _, chunk := range chunks {
			got := streamEval(t, p, doc, chunk)
			assertMatches(t, fmt.Sprintf("%s (chunk=%d)", q, chunk), got, want)
		}
	}
}
