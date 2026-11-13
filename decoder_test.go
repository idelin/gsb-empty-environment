package netsec

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsAbsoluteUnixLinkTarget(t *testing.T) {
	archive, err := BuildMaliciousArchive()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Decode(archive); err == nil {
		t.Fatal("absolute extra-field target was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestFixtureStoresAbsoluteTargetInExtra(t *testing.T) {
	archive, err := BuildMaliciousArchive()
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != 1 {
		t.Fatalf("got %d entries, want 1", len(reader.File))
	}
	target, ok, err := UnixLinkTarget(reader.File[0].Extra)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("Unix extra field has no link target")
	}
	if target != DefaultAbsoluteTarget {
		t.Fatalf("target = %q, want %q", target, DefaultAbsoluteTarget)
	}
	if filepath.IsAbs(target) != true {
		t.Fatalf("target %q is not absolute", target)
	}
}

func TestRejectsRelativeTargetThatEscapesRoot(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name:       "dir/link",
		LinkTarget: "../../outside",
		Mode:       os.ModeSymlink | 0o777,
		Data:       []byte("../../outside"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(archive); err == nil {
		t.Fatal("escaping target was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestAcceptsTargetThatStaysInsideRoot(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name:       "dir/link",
		LinkTarget: "../sibling",
		Mode:       os.ModeSymlink | 0o777,
		Data:       []byte("../sibling"),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(archive)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 {
		t.Fatalf("got %d entries, want 1", len(decoded))
	}
	if decoded[0].LinkTarget != "../sibling" {
		t.Fatalf("target = %q", decoded[0].LinkTarget)
	}
	if filepath.Base(decoded[0].TargetPath) != "sibling" {
		t.Fatalf("resolved target = %q", decoded[0].TargetPath)
	}
}

func TestFallsBackToEntryName(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name: "../escape",
		Data: []byte("file"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(archive); err == nil {
		t.Fatal("unsafe entry name was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestRejectsUnsafeUnicodePath(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name:  "safe",
		Data:  []byte("file"),
		Extra: UnicodePathExtra([]byte("safe"), "../../etc/passwd"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(archive); err == nil {
		t.Fatal("unsafe Unicode path was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestRejectsTargetHiddenAfterNUL(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name:  "safe",
		Data:  []byte("file"),
		Extra: InfoZipUnixExtra("safe\x00../../outside"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(archive); err == nil {
		t.Fatal("NUL-separated escaping target was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestRejectsTargetsInAllUnixLayouts(t *testing.T) {
	ids := []uint16{UnixExtraID, InfoZipUnixExtraID, AsiUnixExtraID}
	for _, id := range ids {
		t.Run(fmt.Sprintf("%04x", id), func(t *testing.T) {
			archive, err := BuildArchive(WriteEntry{
				Name:  "safe",
				Data:  []byte("file"),
				Extra: UnixExtraWithID(id, "../../outside"),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(archive)
			if err == nil {
				t.Fatalf("extra %#x target was accepted", id)
			}
			if !errors.Is(err, ErrPathTraversal) {
				t.Fatalf("extra %#x: got %v", id, err)
			}
		})
	}
}

func TestChecksLocalOnlyUnixExtra(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name:  "link",
		Data:  []byte(DefaultAbsoluteTarget),
		Mode:  os.ModeSymlink | 0o777,
		Extra: InfoZipUnixExtra(DefaultAbsoluteTarget),
	})
	if err != nil {
		t.Fatal(err)
	}
	archive = zeroCentralExtra(t, archive)
	if _, err := Decode(archive); err == nil {
		t.Fatal("local-only Unix target was accepted")
	} else if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("got %v, want ErrPathTraversal", err)
	}
}

func TestAcceptsSafeArchive(t *testing.T) {
	archive, err := BuildArchive(WriteEntry{
		Name: "dir/file",
		Data: []byte("contents"),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].Path != "dir/file" {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestNormalizePath(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		ok   bool
	}{
		{"inside/file", true},
		{"inside/../file", true},
		{"../file", false},
		{"inside/../../file", false},
		{"/absolute", false},
		{`C:\absolute`, false},
		{`..\outside`, false},
		{"inside/./file", true},
	}
	for _, test := range tests {
		_, err := NormalizePath(root, test.name)
		if test.ok && err != nil {
			t.Errorf("NormalizePath(%q) = %v", test.name, err)
		}
		if !test.ok && err == nil {
			t.Errorf("NormalizePath(%q) unexpectedly succeeded", test.name)
		}
	}
}

func TestUnixExtraLayout(t *testing.T) {
	extra := InfoZipUnixExtra("/absolute")
	if len(extra) < 4 {
		t.Fatal("short extra field")
	}
	if id := binary.LittleEndian.Uint16(extra[:2]); id != UnixExtraID {
		t.Fatalf("id = %#x", id)
	}
	size := int(binary.LittleEndian.Uint16(extra[2:4]))
	if size != unixExtraFixed+len("/absolute") {
		t.Fatalf("size = %d", size)
	}
	if got := string(extra[4+unixExtraFixed:]); got != "/absolute" {
		t.Fatalf("tail = %q", got)
	}
}

func zeroCentralExtra(t *testing.T, archive []byte) []byte {
	t.Helper()

	central := bytes.Index(archive, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("central directory not found")
	}
	centralNameLen := int(binary.LittleEndian.Uint16(archive[central+28:]))
	centralExtraLen := int(binary.LittleEndian.Uint16(archive[central+30:]))
	centralExtra := central + 46 + centralNameLen
	for i := 0; i < centralExtraLen; i++ {
		archive[centralExtra+i] = 0
	}
	return archive
}
