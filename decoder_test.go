package jsonpathstream

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEvaluateDecoder(t *testing.T) {
	doc := []byte(`{"a.b":[10,20,30]}`)
	p, err := Parse(`$["a.b"][-1]`)
	if err != nil {
		t.Fatal(err)
	}
	var got []Match
	if err := EvaluateDecoder(p, json.NewDecoder(bytes.NewReader(doc)), func(m Match) error {
		got = append(got, m)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := referenceEval(t, p, doc)
	assertMatches(t, "decoder", got, want)
	if len(got) != 1 || got[0].Value != float64(30) {
		t.Fatalf("got %#v", got)
	}
}
