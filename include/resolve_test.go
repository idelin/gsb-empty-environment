package include

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func doc(id string, trust Trust, settings map[string]string, includes ...string) Document {
	return Document{ID: id, Trust: trust, Settings: settings, Includes: includes}
}

func docsOf(docs ...Document) map[string]Document {
	m := make(map[string]Document, len(docs))
	for _, d := range docs {
		m[d.ID] = d
	}
	return m
}

func mustResolve(t *testing.T, docs map[string]Document, root string) *Result {
	t.Helper()
	r, err := Resolve(docs, root)
	if err != nil {
		t.Fatalf("Resolve: unexpected error: %v", err)
	}
	return r
}

func TestDiamondInclude(t *testing.T) {
	// root includes b and c; both include d. d is expanded once per
	// occurrence and the later occurrence wins the shared key.
	docs := docsOf(
		doc("root", Trusted, map[string]string{"root-key": "r"}, "b", "c"),
		doc("b", Trusted, map[string]string{"b-key": "b"}, "d"),
		doc("c", Trusted, map[string]string{"c-key": "c"}, "d"),
		doc("d", Trusted, map[string]string{"shared": "from-d"}),
	)
	r := mustResolve(t, docs, "root")

	if got := r.Values["shared"]; got != "from-d" {
		t.Fatalf("shared = %q, want %q", got, "from-d")
	}
	// A global by-ID cache would skip d's second expansion and leave the
	// b-side path behind.
	wantPath := []string{"root", "c", "d"}
	if src := r.Sources["shared"]; !reflect.DeepEqual(src.Path, wantPath) || !src.Trusted {
		t.Fatalf("Sources[shared] = %+v, want path %v trusted", src, wantPath)
	}
	for _, key := range []string{"root-key", "b-key", "c-key"} {
		if _, ok := r.Values[key]; !ok {
			t.Fatalf("missing key %q in %v", key, r.Values)
		}
	}
	if len(r.Rejects) != 0 {
		t.Fatalf("unexpected rejections: %+v", r.Rejects)
	}
}

func TestTrustLaundering(t *testing.T) {
	// t is itself trusted but reached through untrusted u, so its writes
	// are downgraded and cannot override a's trusted write.
	docs := docsOf(
		doc("root", Trusted, nil, "a", "u"),
		doc("a", Trusted, map[string]string{"k": "trusted-value"}),
		doc("u", Untrusted, nil, "t"),
		doc("t", Trusted, map[string]string{"k": "laundered", "t-key": "tv"}),
	)
	r := mustResolve(t, docs, "root")

	if got := r.Values["k"]; got != "trusted-value" {
		t.Fatalf("k = %q, want %q", got, "trusted-value")
	}
	wantSrc := Source{Path: []string{"root", "a"}, Trusted: true}
	if !reflect.DeepEqual(r.Sources["k"], wantSrc) {
		t.Fatalf("Sources[k] = %+v, want %+v", r.Sources["k"], wantSrc)
	}
	wantRejects := []Rejection{{
		Key:       "k",
		Value:     "laundered",
		Path:      []string{"root", "u", "t"},
		BlockedBy: []string{"root", "a"},
	}}
	if !reflect.DeepEqual(r.Rejects, wantRejects) {
		t.Fatalf("Rejects = %+v, want %+v", r.Rejects, wantRejects)
	}
	// Downgraded writes still land on keys no trusted path holds.
	if got := r.Values["t-key"]; got != "tv" {
		t.Fatalf("t-key = %q, want %q", got, "tv")
	}
	if src := r.Sources["t-key"]; src.Trusted {
		t.Fatalf("Sources[t-key] = %+v, want untrusted", src)
	}
}

func TestSameValueOverrideRejected(t *testing.T) {
	// An untrusted write carrying the identical value is still an override
	// attempt against a trusted holder and must be rejected and recorded.
	docs := docsOf(
		doc("root", Trusted, nil, "a", "u"),
		doc("a", Trusted, map[string]string{"k": "same"}),
		doc("u", Untrusted, map[string]string{"k": "same"}),
	)
	r := mustResolve(t, docs, "root")

	if got := r.Values["k"]; got != "same" {
		t.Fatalf("k = %q, want %q", got, "same")
	}
	wantSrc := Source{Path: []string{"root", "a"}, Trusted: true}
	if !reflect.DeepEqual(r.Sources["k"], wantSrc) {
		t.Fatalf("Sources[k] = %+v, want %+v", r.Sources["k"], wantSrc)
	}
	if len(r.Rejects) != 1 || r.Rejects[0].Value != "same" {
		t.Fatalf("Rejects = %+v, want one rejection of value %q", r.Rejects, "same")
	}
}

func TestTrustedOverridesUntrusted(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "u", "a"),
		doc("u", Untrusted, map[string]string{"k": "bad"}),
		doc("a", Trusted, map[string]string{"k": "good"}),
	)
	r := mustResolve(t, docs, "root")

	if got := r.Values["k"]; got != "good" {
		t.Fatalf("k = %q, want %q", got, "good")
	}
	if !r.Sources["k"].Trusted {
		t.Fatalf("Sources[k] = %+v, want trusted", r.Sources["k"])
	}
	if len(r.Rejects) != 0 {
		t.Fatalf("unexpected rejections: %+v", r.Rejects)
	}
}

func TestUntrustedOverridesUntrusted(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "u1", "u2"),
		doc("u1", Untrusted, map[string]string{"k": "first"}),
		doc("u2", Untrusted, map[string]string{"k": "second"}),
	)
	r := mustResolve(t, docs, "root")

	if got := r.Values["k"]; got != "second" {
		t.Fatalf("k = %q, want %q", got, "second")
	}
	if len(r.Rejects) != 0 {
		t.Fatalf("unexpected rejections: %+v", r.Rejects)
	}
}

func TestSameDocumentDifferentTrustPaths(t *testing.T) {
	// shared is expanded twice: once downgraded through u, once fully
	// trusted through a. Order decides whether the untrusted occurrence is
	// overwritten or rejected.
	build := func(includes ...string) map[string]Document {
		return docsOf(
			doc("root", Trusted, nil, includes...),
			doc("u", Untrusted, nil, "shared"),
			doc("a", Trusted, nil, "shared"),
			doc("shared", Trusted, map[string]string{"k": "s"}),
		)
	}

	r := mustResolve(t, build("u", "a"), "root")
	wantSrc := Source{Path: []string{"root", "a", "shared"}, Trusted: true}
	if !reflect.DeepEqual(r.Sources["k"], wantSrc) {
		t.Fatalf("trusted-last: Sources[k] = %+v, want %+v", r.Sources["k"], wantSrc)
	}
	if len(r.Rejects) != 0 {
		t.Fatalf("trusted-last: unexpected rejections: %+v", r.Rejects)
	}

	r = mustResolve(t, build("a", "u"), "root")
	if !reflect.DeepEqual(r.Sources["k"], wantSrc) {
		t.Fatalf("untrusted-last: Sources[k] = %+v, want %+v", r.Sources["k"], wantSrc)
	}
	if len(r.Rejects) != 1 {
		t.Fatalf("untrusted-last: Rejects = %+v, want one rejection", r.Rejects)
	}
	if want := []string{"root", "u", "shared"}; !reflect.DeepEqual(r.Rejects[0].Path, want) {
		t.Fatalf("untrusted-last: rejection path = %v, want %v", r.Rejects[0].Path, want)
	}
}

func TestRootSettingsOverrideIncludes(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, map[string]string{"k": "own"}, "a"),
		doc("a", Trusted, map[string]string{"k": "included"}),
	)
	r := mustResolve(t, docs, "root")
	if got := r.Values["k"]; got != "own" {
		t.Fatalf("k = %q, want %q", got, "own")
	}
}

func TestCycle(t *testing.T) {
	tests := []struct {
		name     string
		docs     map[string]Document
		root     string
		wantPath []string
	}{
		{
			name: "self include",
			docs: docsOf(
				doc("a", Trusted, map[string]string{"k": "v"}, "a"),
			),
			root:     "a",
			wantPath: []string{"a", "a"},
		},
		{
			name: "indirect cycle",
			docs: docsOf(
				doc("root", Trusted, nil, "b"),
				doc("b", Trusted, map[string]string{"k": "b"}, "c"),
				doc("c", Untrusted, map[string]string{"k": "c"}, "b"),
			),
			root:     "root",
			wantPath: []string{"root", "b", "c", "b"},
		},
		{
			name: "cycle after writes publishes nothing",
			docs: docsOf(
				doc("root", Trusted, map[string]string{"early": "written"}, "b"),
				doc("b", Trusted, map[string]string{"k": "b"}, "root"),
			),
			root:     "root",
			wantPath: []string{"root", "b", "root"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Resolve(tt.docs, tt.root)
			if r != nil {
				t.Fatalf("Resolve returned partial result %+v alongside error %v", r, err)
			}
			var cycleErr *CycleError
			if !errors.As(err, &cycleErr) {
				t.Fatalf("error = %v, want CycleError", err)
			}
			if !reflect.DeepEqual(cycleErr.Path, tt.wantPath) {
				t.Fatalf("cycle path = %v, want %v", cycleErr.Path, tt.wantPath)
			}
		})
	}
}

func TestUnknownDocument(t *testing.T) {
	docs := docsOf(
		doc("root", Trusted, nil, "a"),
		doc("a", Trusted, nil, "ghost"),
	)
	r, err := Resolve(docs, "root")
	if r != nil {
		t.Fatalf("Resolve returned partial result %+v alongside error %v", r, err)
	}
	var unknownErr *UnknownDocumentError
	if !errors.As(err, &unknownErr) {
		t.Fatalf("error = %v, want UnknownDocumentError", err)
	}
	if unknownErr.ID != "ghost" {
		t.Fatalf("unknown ID = %q, want %q", unknownErr.ID, "ghost")
	}
	if want := []string{"root", "a"}; !reflect.DeepEqual(unknownErr.From, want) {
		t.Fatalf("referrer path = %v, want %v", unknownErr.From, want)
	}

	if _, err := Resolve(docs, "missing-root"); err == nil {
		t.Fatal("Resolve with unknown root: want error, got nil")
	}
}

func TestTooManyDocuments(t *testing.T) {
	docs := make(map[string]Document, MaxDocuments+1)
	for i := 0; i <= MaxDocuments; i++ {
		id := fmt.Sprintf("d%d", i)
		docs[id] = doc(id, Trusted, nil)
	}
	r, err := Resolve(docs, "d0")
	if r != nil {
		t.Fatalf("Resolve returned result %+v alongside error %v", r, err)
	}
	var tooManyErr *TooManyDocumentsError
	if !errors.As(err, &tooManyErr) {
		t.Fatalf("error = %v, want TooManyDocumentsError", err)
	}
	if tooManyErr.Count != MaxDocuments+1 {
		t.Fatalf("Count = %d, want %d", tooManyErr.Count, MaxDocuments+1)
	}
}

func TestIDMismatchRejected(t *testing.T) {
	docs := map[string]Document{
		"root": doc("other", Trusted, nil),
	}
	if _, err := Resolve(docs, "root"); err == nil {
		t.Fatal("Resolve with mismatched ID: want error, got nil")
	}
}
