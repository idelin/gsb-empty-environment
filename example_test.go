package netsec_test

import (
	"testing"

	netsec "example.org/netsec/uid1278"
)

func TestReadmeExample(t *testing.T) {
	doc := netsec.NewDocument([]byte("0123456789"))
	v, err := doc.AddRanges(
		netsec.Range{Start: 2, End: 6, Label: "pii"},
		netsec.Range{Start: 4, End: 9, Label: "secret"},
	)
	if err != nil {
		t.Fatal(err)
	}

	r := v.Reader()
	buf := make([]byte, 10)
	if _, err := r.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "01*******9" {
		t.Fatalf("no-auth view = %q", buf)
	}

	partial := v.Reader("pii")
	if _, err := partial.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "0123*****9" {
		t.Fatalf("partial view = %q", buf)
	}
	if got := partial.Proof().Masked; len(got) != 1 || got[0] != (netsec.Range{Start: 4, End: 9}) {
		t.Fatalf("partial proof = %+v", got)
	}

	if _, err := doc.ReplaceRanges(netsec.Range{Start: 0, End: 1, Label: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "01*******9" {
		t.Fatalf("pinned view drifted = %q", buf)
	}
}
