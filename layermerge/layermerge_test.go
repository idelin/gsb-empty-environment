package layermerge

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	body     string
	linkname string
	typeflag byte
	mode     int64
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Linkname: entry.linkname,
			Typeflag: entry.typeflag,
		}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if entry.typeflag == tar.TypeDir {
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write dir header %s: %v", entry.name, err)
			}
			continue
		}
		if entry.typeflag == tar.TypeSymlink || entry.typeflag == tar.TypeLink {
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write link header %s: %v", entry.name, err)
			}
			continue
		}
		hdr.Size = int64(len(entry.body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write file header %s: %v", entry.name, err)
		}
		if _, err := io.WriteString(tw, entry.body); err != nil {
			t.Fatalf("write file body %s: %v", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func gzipBlob(t *testing.T, tarBytes []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(tarBytes); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func sha256Digest(blob []byte) string {
	sum := sha256.Sum256(blob)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func gzipLayer(t *testing.T, entries []tarEntry) ([]byte, string) {
	t.Helper()
	blob := gzipBlob(t, buildTar(t, entries))
	return blob, sha256Digest(blob)
}

func layerFromBlob(blob []byte, digest, mediaType string) Layer {
	return Layer{
		Digest:    digest,
		MediaType: mediaType,
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(blob)), nil
		},
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed, got err=%v", path, err)
	}
}

func TestMergeLayersOrderingAndWhiteouts(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	work := t.TempDir()

	l1Blob, l1Digest := gzipLayer(t, []tarEntry{
		{name: "etc/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "etc/app.conf", body: "v1"},
		{name: "etc/old.conf", body: "old"},
		{name: "data/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "data/keep.txt", body: "keep"},
		{name: "data/drop.txt", body: "drop"},
	})
	l2Blob, l2Digest := gzipLayer(t, []tarEntry{
		{name: "etc/app.conf", body: "v2"},
		// 普通白化：删除上一层的 etc/old.conf。
		{name: "etc/.wh.old.conf", body: ""},
		// 不透明目录：清空上一层 data/ 下的全部内容。
		{name: "data/.wh..wh..opq", body: ""},
		{name: "data/fresh.txt", body: "fresh"},
	})
	l3Blob, l3Digest := gzipLayer(t, []tarEntry{
		{name: "etc/app.conf", body: "v3"},
	})

	layers := []Layer{
		layerFromBlob(l1Blob, l1Digest, MediaTypeLayerGzip),
		layerFromBlob(l2Blob, l2Digest, MediaTypeLayerGzip),
		layerFromBlob(l3Blob, l3Digest, MediaTypeLayerGzip),
	}
	if err := MergeLayers(root, layers, &Options{WorkDir: work}); err != nil {
		t.Fatalf("MergeLayers: %v", err)
	}

	if got := readFileString(t, filepath.Join(root, "etc/app.conf")); got != "v3" {
		t.Fatalf("app.conf = %q, want v3", got)
	}
	assertNotExists(t, filepath.Join(root, "etc/old.conf"))
	assertNotExists(t, filepath.Join(root, "data/keep.txt"))
	assertNotExists(t, filepath.Join(root, "data/drop.txt"))
	if got := readFileString(t, filepath.Join(root, "data/fresh.txt")); got != "fresh" {
		t.Fatalf("fresh.txt = %q, want fresh", got)
	}
}

func TestRejectReplacedMiddleLayerDigestMismatch(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	work := t.TempDir()

	l1Blob, l1Digest := gzipLayer(t, []tarEntry{
		{name: "base.txt", body: "base"},
	})
	// 原始第二层及其在清单中声明的真实摘要。
	originalL2, originalDigest := gzipLayer(t, []tarEntry{
		{name: "mid.txt", body: "original middle"},
	})
	// 中间层被替换，但摘要仍沿用被替换前的期望值。
	replacedL2, replacedActualDigest := gzipLayer(t, []tarEntry{
		{name: "mid.txt", body: "tampered middle"},
	})
	l3Blob, l3Digest := gzipLayer(t, []tarEntry{
		{name: "top.txt", body: "top"},
	})
	_ = originalL2

	layers := []Layer{
		layerFromBlob(l1Blob, l1Digest, MediaTypeLayerGzip),
		layerFromBlob(replacedL2, originalDigest, MediaTypeLayerGzip),
		layerFromBlob(l3Blob, l3Digest, MediaTypeLayerGzip),
	}
	err := MergeLayers(root, layers, &Options{WorkDir: work})
	if err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}

	var le *LayerError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LayerError, got %T: %v", err, err)
	}
	if le.Index != 2 {
		t.Fatalf("layer index = %d, want 2", le.Index)
	}
	if le.Kind != ErrDigestMismatch {
		t.Fatalf("kind = %q, want %q", le.Kind, ErrDigestMismatch)
	}
	if le.Expected != originalDigest {
		t.Fatalf("expected digest = %q, want %q", le.Expected, originalDigest)
	}
	if le.Got != replacedActualDigest {
		t.Fatalf("actual digest = %q, want %q", le.Got, replacedActualDigest)
	}
	if !strings.Contains(err.Error(), "layer 2") || !strings.Contains(err.Error(), originalDigest) {
		t.Fatalf("error message must carry layer number and expected digest: %v", err)
	}

	// 失败层与其后第三层均不得落地，第一层结果保持不变。
	if got := readFileString(t, filepath.Join(root, "base.txt")); got != "base" {
		t.Fatalf("base.txt = %q, want base", got)
	}
	assertNotExists(t, filepath.Join(root, "mid.txt"))
	assertNotExists(t, filepath.Join(root, "top.txt"))
}

func TestRejectPathEscape(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		path    string
	}{
		{
			name: "dotdot entry name",
			entries: []tarEntry{
				{name: "../evil.txt", body: "escape"},
			},
			path: "../evil.txt",
		},
		{
			name: "nested dotdot entry name",
			entries: []tarEntry{
				{name: "sub/../../evil.txt", body: "escape"},
			},
			path: "sub/../../evil.txt",
		},
		{
			name: "symlink points outside",
			entries: []tarEntry{
				{name: "link", typeflag: tar.TypeSymlink, linkname: "../../../etc"},
			},
			path: "link",
		},
		{
			name: "hardlink points outside",
			entries: []tarEntry{
				{name: "hl", typeflag: tar.TypeLink, linkname: "../../../etc/passwd"},
			},
			path: "hl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			work := t.TempDir()
			blob, digest := gzipLayer(t, tc.entries)
			err := MergeLayers(root,
				[]Layer{layerFromBlob(blob, digest, MediaTypeLayerGzip)},
				&Options{WorkDir: work})
			var le *LayerError
			if !errors.As(err, &le) {
				t.Fatalf("expected *LayerError, got %T: %v", err, err)
			}
			if le.Kind != ErrPathEscape || le.Index != 1 || le.Path != tc.path {
				t.Fatalf("unexpected error: %+v", le)
			}
			assertNotExists(t, filepath.Join(root, filepath.Base(tc.path)))
		})
	}
}

func TestRejectCompressionFormatMismatch(t *testing.T) {
	// blob 实际是未压缩 tar，却声明 gzip MediaType。
	root := filepath.Join(t.TempDir(), "root")
	tarBlob := buildTar(t, []tarEntry{{name: "f.txt", body: "x"}})
	err := MergeLayers(root,
		[]Layer{layerFromBlob(tarBlob, sha256Digest(tarBlob), MediaTypeLayerGzip)},
		&Options{WorkDir: t.TempDir()})
	var le *LayerError
	if !errors.As(err, &le) {
		t.Fatalf("expected *LayerError, got %T: %v", err, err)
	}
	if le.Kind != ErrFormatMismatch || le.Index != 1 {
		t.Fatalf("unexpected error: %+v", le)
	}

	// 反向：gzip blob 却声明未压缩 tar。
	gzBlob, gzDigest := gzipLayer(t, []tarEntry{{name: "f.txt", body: "x"}})
	err = MergeLayers(filepath.Join(t.TempDir(), "root2"),
		[]Layer{layerFromBlob(gzBlob, gzDigest, MediaTypeLayerTar)},
		&Options{WorkDir: t.TempDir()})
	if !errors.As(err, &le) {
		t.Fatalf("expected *LayerError, got %T: %v", err, err)
	}
	if le.Kind != ErrFormatMismatch {
		t.Fatalf("unexpected error: %+v", le)
	}
}

func TestRejectInvalidDigest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	work := t.TempDir()
	blob, _ := gzipLayer(t, []tarEntry{{name: "f.txt", body: "x"}})

	for _, bad := range []string{"sha256:nope", "not-a-digest", "sha1:abc"} {
		err := MergeLayers(root,
			[]Layer{layerFromBlob(blob, bad, MediaTypeLayerGzip)},
			&Options{WorkDir: work})
		var le *LayerError
		if !errors.As(err, &le) || le.Kind != ErrInvalidDigest {
			t.Fatalf("digest %q: expected invalid digest error, got %v", bad, err)
		}
	}
}

func TestUncompressedTarLayerAccepted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	work := t.TempDir()
	tarBlob := buildTar(t, []tarEntry{
		{name: "dir/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "dir/hello.txt", body: "hello"},
	})
	layers := []Layer{
		layerFromBlob(tarBlob, sha256Digest(tarBlob), MediaTypeLayerTar),
	}
	if err := MergeLayers(root, layers, &Options{WorkDir: work}); err != nil {
		t.Fatalf("MergeLayers: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "dir/hello.txt")); got != "hello" {
		t.Fatalf("hello.txt = %q", got)
	}
}

func TestLayerErrorMessageIsInformative(t *testing.T) {
	blob, _ := gzipLayer(t, []tarEntry{{name: "f.txt", body: "x"}})
	wrongDigest := sha256Digest(append(append([]byte{}, blob...), 0))
	err := MergeLayers(t.TempDir(),
		[]Layer{layerFromBlob(blob, wrongDigest, MediaTypeLayerGzip)},
		&Options{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"layer 1", "digest mismatch", "expected sha256:", "got sha256:"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}
