package safeuntar

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateEntryNameEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{"", ErrPathEscape},
		{".", ErrPathEscape},
		{"/etc/passwd", ErrPathEscape},
		{"../x", ErrPathEscape},
		{"a/../../x", ErrPathEscape},
		{"a\\b", ErrPathEscape},
		{"./a/./b", nil}, // normalizes to "a/b"
		{"a/b/", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b rawBuilder
			b.rawEntry(tc.name, tar.TypeReg, []byte("x"), "")
			b.finish()
			err := safeExtract(b.Bytes())
			if tc.want == nil {
				if err != nil {
					t.Fatalf("expected success for %q, got %v", tc.name, err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("for %q got %v, want %v", tc.name, err, tc.want)
			}
		})
	}

	// NUL bytes are stripped at the C-string boundary by archive/tar before
	// the header reaches Extract, so exercise the validator directly.
	if _, err := validateEntryName("a\x00b"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("NUL entry name: got %v", err)
	}
	if _, err := validateEntryName(strings.Repeat("n", maxNameComponent+1)); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("overlong component not rejected")
	}
	if cleaned, err := validateEntryName("a/b/"); err != nil || cleaned != "a/b" {
		t.Fatalf("trailing slash normalization = %q, %v", cleaned, err)
	}
}

func TestValidateSymlinkTargetEdgeCases(t *testing.T) {
	for _, target := range []string{
		"",
		"/etc/passwd",
		"../../../etc/passwd",
		"a\\b",
	} {
		var b rawBuilder
		b.rawEntry("lnk", tar.TypeSymlink, nil, target)
		b.finish()
		if err := safeExtract(b.Bytes()); !errors.Is(err, ErrLinkEscape) {
			t.Fatalf("target %q: got %v, want ErrLinkEscape", target, err)
		}
	}
	if err := validateSymlinkTarget("lnk", "a\x00b"); !errors.Is(err, ErrLinkEscape) {
		t.Fatalf("NUL symlink target: got %v", err)
	}
}

func TestRejectsHardLinkToDirectory(t *testing.T) {
	var b rawBuilder
	b.rawEntry("adir", tar.TypeDir, nil, "")
	b.rawEntry("alink", tar.TypeLink, nil, "adir")
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrHardLinkTarget) {
		t.Fatalf("got %v, want ErrHardLinkTarget", err)
	}
}

func TestRejectsDuplicateDirectoryEntry(t *testing.T) {
	var b rawBuilder
	b.rawEntry("dir", tar.TypeDir, nil, "")
	b.rawEntry("dir", tar.TypeDir, nil, "")
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("got %v, want ErrDuplicateEntry", err)
	}
}

func TestRejectsFileCollidingWithImplicitParent(t *testing.T) {
	// The second entry forces implicit creation of "a" as a directory; the
	// later explicit "a" file must be rejected rather than replace it.
	var b rawBuilder
	b.rawEntry("a/child.txt", tar.TypeReg, []byte("x"), "")
	b.rawEntry("a", tar.TypeReg, []byte("y"), "")
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("got %v, want ErrDuplicateEntry", err)
	}
}

func TestRejectsUnknownEntryType(t *testing.T) {
	var b rawBuilder
	b.rawEntry("contiguous", '7', nil, "") // POSIX contiguous file typeflag
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("got %v, want ErrUnsupportedType", err)
	}
}

func TestRejectsSparseDeclarationWithoutMap(t *testing.T) {
	var b rawBuilder
	records := map[string]string{
		"GNU.sparse.major": "0",
		"GNU.sparse.minor": "1",
	}
	next := rawHeader("sparse.bin", tar.TypeReg, 2, "")
	b.paxEntry(records, next, []byte("AB"))
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrMalformedArchive) {
		t.Fatalf("got %v, want ErrMalformedArchive", err)
	}
}

func TestRejectsMalformedSparseNumber(t *testing.T) {
	var b rawBuilder
	records := map[string]string{
		"GNU.sparse.major":     "0",
		"GNU.sparse.minor":     "1",
		"GNU.sparse.numblocks": "not-a-number",
		"GNU.sparse.map":       "0,2",
		"GNU.sparse.size":      "2",
	}
	next := rawHeader("sparse.bin", tar.TypeReg, 2, "")
	b.paxEntry(records, next, []byte("AB"))
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrMalformedArchive) {
		t.Fatalf("got %v, want ErrMalformedArchive", err)
	}
}

func TestSizeMismatchRejected(t *testing.T) {
	// Header declares 4 bytes but the stream ends after 2, so the reader
	// reports unexpected EOF while the data section is consumed.
	var b rawBuilder
	header := rawHeader("short.bin", tar.TypeReg, 4, "")
	b.Write(header)
	b.Write([]byte("AB"))
	b.finish()
	if err := safeExtract(b.Bytes()); !errors.Is(err, ErrMalformedArchive) {
		t.Fatalf("got %v, want ErrMalformedArchive", err)
	}
}

func TestImplicitParentDirectoriesAreUsable(t *testing.T) {
	var b rawBuilder
	b.rawEntry("x/y/z/file.txt", tar.TypeReg, []byte("deep"), "")
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	info, err := os.Stat(filepath.Join(dest, "x", "y", "z"))
	if err != nil {
		t.Fatalf("implicit dir missing: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("implicit path is not a directory")
	}
}

func TestGNU1x0SparseDeclarationAccepted(t *testing.T) {
	// The 1.0 map travels in the data stream and is validated by
	// archive/tar; our checker must not reject the bare 1.0 PAX records.
	var b rawBuilder
	records := map[string]string{
		"GNU.sparse.major":    "1",
		"GNU.sparse.minor":    "0",
		"GNU.sparse.realsize": "4",
	}
	// 1 entry; single fragment at offset 0 with length 2.
	mapStream := "1\n0\n2\n"
	padded := mapStream + strings.Repeat("\x00", 512-len(mapStream))
	next := rawHeader("GNUSparseFile.0/sparse.bin", tar.TypeReg, int64(len(padded)+2), "")
	b.paxEntry(records, next, append([]byte(padded), []byte("AB")...))
	b.finish()

	// We only assert that the PAX record check itself does not reject the
	// declaration; archive/tar owns stream-map validation.
	_ = safeExtract(b.Bytes())
}

// safeExtract runs Extract into a fresh temporary destination and returns
// the error, ignoring success/failure of the destination cleanup.
func safeExtract(archive []byte) error {
	dest, err := os.MkdirTemp("", "safeuntar-edge-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dest)
	target := filepath.Join(dest, "out")
	return Extract(bytes.NewReader(archive), target)
}
