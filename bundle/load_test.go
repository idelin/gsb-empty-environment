package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseValidBundlePlainAndGzip(t *testing.T) {
	body := []byte(`{"v":1}`)
	for _, gzipped := range []bool{false, true} {
		archive := validBundle(t, "team-a", "team-a/policies/x.json", body, gzipped)
		b, err := Parse("b1", bytes.NewReader(archive))
		if err != nil {
			t.Fatalf("gzipped=%v: parse: %v", gzipped, err)
		}
		if b.Name() != "b1" {
			t.Fatalf("gzipped=%v: name = %q", gzipped, b.Name())
		}
		got, ok := b.Doc("/data/team-a/policies/x.json")
		if !ok || !bytes.Equal(got, body) {
			t.Fatalf("gzipped=%v: doc = %q, ok=%v", gzipped, got, ok)
		}
		if len(b.Roots()) != 1 || b.Roots()[0] != "team-a" {
			t.Fatalf("gzipped=%v: roots = %v", gzipped, b.Roots())
		}
	}
}

func TestSegmentPrefixDoesNotMatchAcrossSegments(t *testing.T) {
	// Root "a/b" must own "a/b/c" but must not own "a/bc".
	inside := entry{name: "data/a/b/c.json", content: []byte("inside")}
	if _, err := Parse("ok", bytes.NewReader(buildArchive(t, false,
		manifestEntry(t, []string{"a/b"}, inside), inside))); err != nil {
		t.Fatalf("file under root rejected: %v", err)
	}

	outside := entry{name: "data/a/bc.json", content: []byte("outside")}
	_, err := Parse("bad", bytes.NewReader(buildArchive(t, false,
		manifestEntry(t, []string{"a/b"}, outside), outside)))
	assertErrorIs(t, err, ErrOutsideRoot)

	// String-prefix matching would also wrongly accept "a/bc/c".
	deeper := entry{name: "data/a/bc/c.json", content: []byte("deeper")}
	_, err = Parse("bad2", bytes.NewReader(buildArchive(t, false,
		manifestEntry(t, []string{"a/b"}, deeper), deeper)))
	assertErrorIs(t, err, ErrOutsideRoot)
}

func TestEmptyRootMatchesEveryDataPath(t *testing.T) {
	doc := entry{name: "data/anything/at/all.json", content: []byte("{}")}
	b, err := Parse("global", bytes.NewReader(buildArchive(t, false,
		manifestEntry(t, []string{""}, doc), doc)))
	if err != nil {
		t.Fatalf("empty root should match all paths: %v", err)
	}
	if b.Roots()[0] != "" {
		t.Fatalf("root = %q, want empty", b.Roots()[0])
	}
}

func TestMissingRootsRejected(t *testing.T) {
	doc := entry{name: "data/a/x.json", content: []byte("{}")}
	_, err := Parse("noroots", bytes.NewReader(buildArchive(t, false,
		manifestEntry(t, nil, doc), doc)))
	assertErrorIs(t, err, ErrRootEmpty)
}

func TestOverlappingBundlesRejected(t *testing.T) {
	l := NewLoader()

	first := validBundle(t, "team/a", "team/a/x.json", []byte("1"), false)
	if _, err := l.Load("first", bytes.NewReader(first)); err != nil {
		t.Fatalf("first load: %v", err)
	}

	// Distinct team prefixes: disjoint roots are accepted.
	other := validBundle(t, "team/b", "team/b/x.json", []byte("2"), false)
	if _, err := l.Load("other", bytes.NewReader(other)); err != nil {
		t.Fatalf("disjoint roots rejected: %v", err)
	}

	cases := []struct {
		name string
		root string
		rel  string
	}{
		{"same-root", "team/a", "team/a/y.json"},
		{"parent-root", "team", "team/a/y.json"},
		{"child-root", "team/a/sub", "team/a/sub/y.json"},
	}
	for _, tc := range cases {
		archive := validBundle(t, tc.root, tc.rel, []byte("3"), false)
		_, err := l.Load(tc.name, bytes.NewReader(archive))
		assertErrorIs(t, err, ErrRootOverlap)
		if _, ok := l.Bundle(tc.name); ok {
			t.Fatalf("%s: rejected bundle was installed", tc.name)
		}
	}

	// "team-ab" only shares a string prefix with "team/a"; by segment it
	// is disjoint and must be accepted, then unloaded again.
	sibling := validBundle(t, "team-ab", "team-ab/y.json", []byte("3"), false)
	if _, err := l.Load("sibling", bytes.NewReader(sibling)); err != nil {
		t.Fatalf("segment-disjoint root rejected: %v", err)
	}
	l.Unload("sibling")

	// The empty (global) root overlaps every other bundle.
	global := validBundle(t, "", "anywhere/y.json", []byte("4"), false)
	_, err := l.Load("global", bytes.NewReader(global))
	assertErrorIs(t, err, ErrRootOverlap)

	if len(l.Names()) != 2 {
		t.Fatalf("active bundles = %v, want first and other only", l.Names())
	}
}

func TestTwoEmptyRootBundlesOverlap(t *testing.T) {
	l := NewLoader()
	first := validBundle(t, "", "a/x.json", []byte("1"), false)
	if _, err := l.Load("first", bytes.NewReader(first)); err != nil {
		t.Fatalf("first load: %v", err)
	}
	second := validBundle(t, "", "b/x.json", []byte("2"), false)
	_, err := l.Load("second", bytes.NewReader(second))
	assertErrorIs(t, err, ErrRootOverlap)
}

func TestDigestMismatchRejectsWholeBundle(t *testing.T) {
	doc := entry{name: "data/team-a/x.json", content: []byte("original")}
	man := manifestEntry(t, []string{"team-a"}, doc)

	tampered := entry{name: doc.name, content: []byte("tampered")}
	_, err := Parse("evil", bytes.NewReader(buildArchive(t, false, man, tampered)))
	assertErrorIs(t, err, ErrDigestMismatch)

	// A wrong digest embedded in the manifest must also be rejected.
	badMan := manifestEntry(t, []string{"team-a"}, doc)
	var m Manifest
	if err := json.Unmarshal(badMan.content, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	m.FileDigests["/data/team-a/x.json"] = "00" + digestOf(doc.content)[2:]
	raw, _ := json.Marshal(m)
	badMan.content = raw
	_, err = Parse("evil2", bytes.NewReader(buildArchive(t, false, badMan, doc)))
	assertErrorIs(t, err, ErrDigestMismatch)
}

func TestMissingAndUnexpectedFilesRejected(t *testing.T) {
	doc := entry{name: "data/team-a/x.json", content: []byte("{}")}
	extra := entry{name: "data/team-a/y.json", content: []byte("{}")}
	man := manifestEntry(t, []string{"team-a"}, doc)

	// Extra file present but not listed in the manifest.
	if _, err := Parse("extra", bytes.NewReader(buildArchive(t, false, man, doc, extra))); !errors.Is(err, ErrUnexpectedFile) {
		t.Fatalf("extra file: want ErrUnexpectedFile, got %v", err)
	}

	// Manifest lists a file that the archive does not contain.
	manMissing := manifestEntry(t, []string{"team-a"}, doc, extra)
	_, err := Parse("missing", bytes.NewReader(buildArchive(t, false, manMissing, doc)))
	assertErrorIs(t, err, ErrMissingFile)
}

func TestFailedLoadKeepsPreviousVersion(t *testing.T) {
	l := NewLoader()
	key := "/data/team-a/x.json"

	v1 := validBundle(t, "team-a", "team-a/x.json", []byte("v1"), false)
	if _, err := l.Load("team-a", bytes.NewReader(v1)); err != nil {
		t.Fatalf("v1 load: %v", err)
	}

	// A corrupt replacement must not displace v1.
	bad := validBundle(t, "team-a", "team-a/x.json", []byte("v2"), false)
	bad[len(bad)-7] ^= 0xff
	if _, err := l.Load("team-a", bytes.NewReader(bad)); err == nil {
		t.Fatal("corrupt archive unexpectedly accepted")
	}
	if got, owner, ok := l.LookupDoc(key); !ok || owner != "team-a" || string(got) != "v1" {
		t.Fatalf("after corrupt load: got %q owner=%q ok=%v, want v1", got, owner, ok)
	}

	// A valid replacement with overlapping roots also fails atomically.
	evil := validBundle(t, "team-a/sub", "team-a/sub/x.json", []byte("v2"), false)
	_, err := l.Load("evil", bytes.NewReader(evil))
	assertErrorIs(t, err, ErrRootOverlap)
	if got, _, ok := l.LookupDoc(key); !ok || string(got) != "v1" {
		t.Fatalf("after overlap load: got %q ok=%v, want v1", got, ok)
	}

	// A well-formed replacement of the same name takes effect.
	v2 := validBundle(t, "team-a", "team-a/x.json", []byte("v2"), false)
	if _, err := l.Load("team-a", bytes.NewReader(v2)); err != nil {
		t.Fatalf("v2 load: %v", err)
	}
	if got, _, ok := l.LookupDoc(key); !ok || string(got) != "v2" {
		t.Fatalf("after valid reload: got %q ok=%v, want v2", got, ok)
	}
}
