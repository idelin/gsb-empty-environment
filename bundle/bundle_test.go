package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

// entry is one file in a test archive.
type entry struct {
	name    string
	content []byte
	symlink bool
}

// buildArchive creates a (optionally gzip-compressed) tar stream from the
// supplied entries.
func buildArchive(t *testing.T, gzipped bool, entries ...entry) []byte {
	t.Helper()

	var buf bytes.Buffer
	var w io.WriteCloser = &nopCloser{&buf}
	if gzipped {
		w = gzip.NewWriter(&buf)
	}
	tw := tar.NewWriter(w)

	for _, e := range entries {
		if e.symlink {
			hdr := &tar.Header{
				Name:     e.name,
				Linkname: string(e.content),
				Mode:     0o777,
			}
			if err := tw.WriteHeader(&tar.Header{
				Name:     hdr.Name,
				Linkname: hdr.Linkname,
				Mode:     hdr.Mode,
				Typeflag: tar.TypeSymlink,
			}); err != nil {
				t.Fatalf("write symlink header: %v", err)
			}
			continue
		}
		hdr := &tar.Header{
			Name: e.name,
			Mode: 0o644,
			Size: int64(len(e.content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.content); err != nil {
			t.Fatalf("write content %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return buf.Bytes()
}

type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

// digestOf returns the lowercase hex sha256 of content.
func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// dataKey converts a data-relative path into a manifest key.
func dataKey(rel string) string { return "/data/" + rel }

// manifestEntry builds a .manifest entry with correct digests for the
// supplied data entries.
func manifestEntry(t *testing.T, roots []string, dataEntries ...entry) entry {
	t.Helper()
	digests := make(map[string]string, len(dataEntries))
	for _, e := range dataEntries {
		digests["/"+e.name] = digestOf(e.content)
	}
	raw, err := json.Marshal(Manifest{Roots: roots, FileDigests: digests})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return entry{name: ManifestName, content: raw}
}

// validBundle assembles a complete, valid bundle for root with one
// document at rel.
func validBundle(t *testing.T, root, rel string, body []byte, gzipped bool) []byte {
	t.Helper()
	doc := entry{name: dataPrefix + rel, content: body}
	man := manifestEntry(t, []string{root}, doc)
	return buildArchive(t, gzipped, man, doc)
}

// assertErrorIs fails unless err wraps target.
func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error wrapping %v, got %v", target, err)
	}
}

// withDirectory builds an archive that also contains a directory entry.
func withDirectory(t *testing.T, man entry, docs ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dir := &tar.Header{Name: "data/team-a/", Mode: 0o755, Typeflag: tar.TypeDir}
	if err := tw.WriteHeader(dir); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	for _, e := range append([]entry{man}, docs...) {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if _, err := tw.Write(e.content); err != nil {
			t.Fatalf("write %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func jsonUnmarshalHelper(t *testing.T, raw []byte, v any) error {
	t.Helper()
	return json.Unmarshal(raw, v)
}

func jsonMarshalHelper(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
