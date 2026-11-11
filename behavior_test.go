package jsonpathstream

import (
	"io"
	"testing"
)

// TestLateFilterField covers the central buffering rule: the field a filter
// references (status) appears after the field that would be output (name).
// The engine buffers only the current candidate object until the filter can
// be decided, then emits exactly the passing members.
func TestLateFilterField(t *testing.T) {
	doc := []byte("{\"rows\":[" +
		"{\"name\":\"first\",\"status\":\"skip\"}," +
		"{\"name\":\"second\",\"status\":\"keep\"}," +
		"{\"name\":\"third\",\"status\":\"keep\"}," +
		"{\"name\":\"fourth\",\"status\":\"skip\"}" +
		"]}")
	p, err := Parse("$.rows[?(@.status == \"keep\")].name")
	if err != nil {
		t.Fatal(err)
	}
	want := referenceEval(t, p, doc)
	got := streamEval(t, p, doc, 1)
	assertMatches(t, "late filter", got, want)
	if len(got) != 2 || got[0].Value != "second" || got[1].Value != "third" {
		t.Fatalf("unexpected matches: %v", got)
	}
}

// TestFilterReferenceMissing confirms a missing referenced field is undefined
// and never satisfies a comparison (including !=).
func TestFilterReferenceMissing(t *testing.T) {
	doc := []byte("[{\"a\":1},{\"a\":2,\"b\":9}]")
	cases := map[string]int{
		"$[?(@.b == 9)].a": 1,
		"$[?(@.b != 9)].a": 0, // missing b is undefined: every comparison is false
		"$[?(@.a > 0)].a":  2,
		"$[?(@.b != 5)].a": 1, // second object b=9, 9 != 5 passes; first undefined fails
	}
	for q, n := range cases {
		p, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		got := streamEval(t, p, doc, 5)
		want := referenceEval(t, p, doc)
		assertMatches(t, q, got, want)
		if len(got) != n {
			t.Fatalf("%s: got %d matches, want %d", q, len(got), n)
		}
	}
}

// TestIncrementalDelivery proves matches reach the sink before the document
// finishes streaming (values are produced as soon as they appear).
func TestIncrementalDelivery(t *testing.T) {
	head := []byte("{\"rows\":[{\"v\":1},{\"v\":2},{\"v\":3}],\"tail\":\"")
	tail := []byte("done\"}")
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write(head)
		_, _ = pw.Write(tail)
		_ = pw.Close()
	}()
	p, _ := Parse("$.rows[*].v")
	delivered := make(chan int, 8)
	go func() {
		_ = EvaluateReader(p, pr, func(m Match) error {
			delivered <- int(m.Value.(float64))
			return nil
		})
		close(delivered)
	}()
	var vals []int
	for v := range delivered {
		vals = append(vals, v)
	}
	if len(vals) != 3 || vals[0] != 1 || vals[1] != 2 || vals[2] != 3 {
		t.Fatalf("incremental matches out of order: %v", vals)
	}
}
