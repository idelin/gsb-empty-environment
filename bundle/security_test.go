package bundle

import (
	"bytes"
	"testing"
)

func TestPathTraversalRejected(t *testing.T) {
	doc := entry{name: "data/team-a/x.json", content: []byte("{}")}
	man := manifestEntry(t, []string{"team-a"}, doc)

	cases := []entry{
		{name: "data/team-a/../../etc/passwd", content: []byte("x")},
		{name: "data/../outside.json", content: []byte("x")},
		{name: "/data/team-a/abs.json", content: []byte("x")},
		{name: "data/team-a/back\\slash.json", content: []byte("x")},
		{name: "secret.txt", content: []byte("x")},
	}
	for _, evil := range cases {
		_, err := Parse("evil", bytes.NewReader(buildArchive(t, false, man, doc, evil)))
		if err == nil {
			t.Fatalf("entry %q unexpectedly accepted", evil.name)
		}
	}
}

func TestSymlinkRejected(t *testing.T) {
	doc := entry{name: "data/team-a/x.json", content: []byte("{}")}
	man := manifestEntry(t, []string{"team-a"}, doc)
	link := entry{name: "data/team-a/link.json", content: []byte("../team-b/secret.json"), symlink: true}

	_, err := Parse("evil", bytes.NewReader(buildArchive(t, false, man, doc, link)))
	assertErrorIs(t, err, ErrUnsupportedEntry)
}

func TestManifestDigestKeyNormalization(t *testing.T) {
	// Manifest keys with a leading slash and internal "." segments resolve
	// to the same canonical key as the tar entry.
	doc := entry{name: "data/team-a/x.json", content: []byte("{}")}
	man := manifestEntry(t, []string{"team-a"}, doc)
	var m Manifest
	if err := jsonUnmarshalHelper(t, man.content, &m); err != nil {
		t.Fatal(err)
	}
	m.FileDigests["/data/team-a/./x.json"] = m.FileDigests["/data/team-a/x.json"]
	delete(m.FileDigests, "/data/team-a/x.json")
	man.content = jsonMarshalHelper(t, m)

	b, err := Parse("norm", bytes.NewReader(buildArchive(t, false, man, doc)))
	if err != nil {
		t.Fatalf("normalized digest key rejected: %v", err)
	}
	if _, ok := b.Doc("/data/team-a/x.json"); !ok {
		t.Fatal("document not stored under canonical key")
	}
}

func TestLookupRoutesToOwningBundle(t *testing.T) {
	l := NewLoader()
	a := validBundle(t, "team-a", "team-a/x.json", []byte("a"), false)
	b := validBundle(t, "team-b", "team-b/x.json", []byte("b"), false)
	if _, err := l.Load("bundle-a", bytes.NewReader(a)); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if _, err := l.Load("bundle-b", bytes.NewReader(b)); err != nil {
		t.Fatalf("load b: %v", err)
	}

	got, owner, ok := l.LookupDoc("/data/team-b/x.json")
	if !ok || owner != "bundle-b" || string(got) != "b" {
		t.Fatalf("lookup = %q owner=%q ok=%v", got, owner, ok)
	}
	if _, _, ok := l.LookupDoc("/data/team-c/x.json"); ok {
		t.Fatal("unknown document unexpectedly found")
	}
	if _, _, ok := l.LookupDoc("team-a/x.json"); ok {
		t.Fatal("non-canonical key unexpectedly accepted")
	}

	if !l.Unload("bundle-b") {
		t.Fatal("unload reported missing bundle")
	}
	if _, _, ok := l.LookupDoc("/data/team-b/x.json"); ok {
		t.Fatal("document still served after unload")
	}
}

func TestDirectoriesAndDuplicateEntries(t *testing.T) {
	// Directory entries are harmless and must not be treated as files.
	doc := entry{name: "data/team-a/x.json", content: []byte("{}")}
	archive := withDirectory(t, manifestEntry(t, []string{"team-a"}, doc), doc)
	if _, err := Parse("dirs", bytes.NewReader(archive)); err != nil {
		t.Fatalf("directory entry rejected: %v", err)
	}

	dupe := buildArchive(t, false,
		manifestEntry(t, []string{"team-a"}, doc), doc, doc)
	if _, err := Parse("dupe", bytes.NewReader(dupe)); err == nil {
		t.Fatal("duplicate entry unexpectedly accepted")
	}
}
