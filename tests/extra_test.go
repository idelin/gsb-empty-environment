package oci_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"testing"

	oci "formal.local/w27-7184be2212"
)

func TestGzipLayerAndStandardFS(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	content := []byte("secret-config")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "etc/app.conf",
		Typeflag: tar.TypeReg,
		Mode:     0o600,
		Size:     int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	layer, err := oci.LoadLayer(bytes.NewReader(gz.Bytes()))
	if err != nil {
		t.Fatalf("LoadLayer gzip: %v", err)
	}
	merged, err := oci.Merge(*layer)
	if err != nil {
		t.Fatal(err)
	}

	data, err := fs.ReadFile(merged, "etc/app.conf")
	if err != nil {
		t.Fatalf("fs.ReadFile: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("content = %q, want %q", data, content)
	}
	entries, err := merged.ReadDir("etc")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "app.conf" {
		t.Fatalf("entries = %#v", entries)
	}
}
