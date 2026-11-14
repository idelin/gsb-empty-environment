package depverify

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"path"
	"sort"
)

// zipReadCloser adapts an in-memory zip to io.ReadCloser while retaining
// random access for HashZip.
type zipReadCloser struct {
	*bytes.Reader
}

func (zipReadCloser) Close() error { return nil }

// moduleBlob is a fetched module: its zip bytes, go.mod contents and the
// source descriptor the fake fetcher reports.
type moduleBlob struct {
	zip    []byte
	goMod  []byte
	source Source
}

// fakeFetcher serves prebuilt modules and records what was requested, so tests
// can assert which effective path@version the verifier actually fetched.
type fakeFetcher struct {
	blobs   map[string]*moduleBlob
	fetched []string
	fail    map[string]error
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{blobs: map[string]*moduleBlob{}, fail: map[string]error{}}
}

func (f *fakeFetcher) key(p, v string) string { return p + "@" + v }

func (f *fakeFetcher) add(modulePath, version string, files map[string]string, src Source) (*moduleBlob, error) {
	blob, err := buildBlob(modulePath, version, files, src)
	if err != nil {
		return nil, err
	}
	f.blobs[f.key(modulePath, version)] = blob
	return blob, nil
}

func (f *fakeFetcher) Fetch(_ context.Context, modulePath, version string) (*Content, error) {
	k := f.key(modulePath, version)
	f.fetched = append(f.fetched, k)
	if err := f.fail[k]; err != nil {
		return nil, err
	}
	blob, ok := f.blobs[k]
	if !ok {
		return nil, fmt.Errorf("fakeFetcher: no blob for %s", k)
	}
	return &Content{
		Zip:     zipReadCloser{bytes.NewReader(blob.zip)},
		ZipSize: int64(len(blob.zip)),
		GoMod:   append([]byte(nil), blob.goMod...),
		Source:  blob.source,
	}, nil
}

func buildBlob(modulePath, version string, files map[string]string, src Source) (*moduleBlob, error) {
	prefix := modulePath + "@" + version + "/"
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(path.Join(prefix, n))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	zipBytes := buf.Bytes()
	goMod := []byte(files["go.mod"])
	return &moduleBlob{
		zip:    zipBytes,
		goMod:  goMod,
		source: src,
	}, nil
}

// sumLines renders go.sum entries (module and go.mod) for a blob.
func sumLines(modulePath, version string, blob *moduleBlob) (string, error) {
	digest, err := HashZip(bytes.NewReader(blob.zip), int64(len(blob.zip)))
	if err != nil {
		return "", err
	}
	lines := fmt.Sprintf("%s %s %s\n", modulePath, version, digest)
	if len(blob.goMod) > 0 {
		lines += fmt.Sprintf("%s %s/go.mod %s\n", modulePath, version, HashBytes(blob.goMod))
	}
	return lines, nil
}

func httpsSource(host, modulePath, version string) Source {
	u := fmt.Sprintf("https://%s/%s/@v/%s.zip", host, modulePath, version)
	return Source{URL: u, Protocol: "https"}
}
