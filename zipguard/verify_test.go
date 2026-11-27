package zipguard

import (
	"bytes"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func u32p(v uint32) *uint32 { return &v }
func u16p(v uint16) *uint16 { return &v }

func regularSpec(name string, content []byte) specEntry {
	return specEntry{
		name:          name,
		content:       content,
		method:        methodDeflate,
		hostSystem:    3,
		externalAttrs: unixAttrs(fs.ModePerm),
	}
}

func TestValidArchiveStoredAndDeflate(t *testing.T) {
	specs := []specEntry{
		{name: "a.txt", content: []byte("hello world"), method: methodStored, hostSystem: 3, externalAttrs: 0o644 << 16},
		regularSpec("dir/b.txt", []byte(strings.Repeat("deflated-", 100))),
		{name: "dir/", content: nil, method: methodStored, hostSystem: 3, externalAttrs: unixAttrs(fs.ModeDir | 0o755)},
	}
	arc := buildArchive(specs, []byte("comment"))
	rep, err := VerifyBytes(arc, testPolicy())
	if err != nil {
		t.Fatalf("expected valid archive, got %v", err)
	}
	if len(rep.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(rep.Entries))
	}
	if rep.TotalUncompressed <= 0 {
		t.Fatalf("total uncompressed not counted: %d", rep.TotalUncompressed)
	}
	rep2, err := VerifyReader(bytes.NewReader(arc), testPolicy())
	if err != nil || len(rep2.Entries) != 3 {
		t.Fatalf("VerifyReader: rep=%v err=%v", rep2, err)
	}
}

func TestValidEmptyArchive(t *testing.T) {
	arc := buildArchive(nil, nil)
	if len(arc) != eocdFixedSize {
		t.Fatalf("empty archive size = %d", len(arc))
	}
	rep, err := VerifyBytes(arc, testPolicy())
	if err != nil {
		t.Fatalf("empty archive rejected: %v", err)
	}
	if len(rep.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(rep.Entries))
	}
}

func TestEOCDMissingAndTruncated(t *testing.T) {
	if _, err := VerifyBytes([]byte("not a zip at all"), testPolicy()); !errors.Is(err, ErrEOCDNotFound) {
		t.Fatalf("err = %v, want ErrEOCDNotFound", err)
	}
	arc := buildArchive([]specEntry{regularSpec("a", []byte("x"))}, nil)
	if _, err := VerifyBytes(arc[:len(arc)-5], testPolicy()); !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrEOCDNotFound) {
		t.Fatalf("truncated: err = %v", err)
	}
}

func TestTrailingDataAfterEOCD(t *testing.T) {
	arc := buildArchive([]specEntry{regularSpec("a", []byte("x"))}, nil)
	evil := append(append([]byte(nil), arc...), 0x41, 0x42, 0x43)
	_, err := VerifyBytes(evil, testPolicy())
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("err = %v, want ErrTrailingData", err)
	}
}

func TestForgedEOCDInComment(t *testing.T) {
	arc := buildArchive(nil, make([]byte, 40))
	sig := make([]byte, 4)
	put32(sig, sigEOCD)
	copy(arc[eocdFixedSize+10:], sig)
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrMultipleEOCD) {
		t.Fatalf("err = %v, want ErrMultipleEOCD", err)
	}
}

func TestMultipleEOCDRecords(t *testing.T) {
	inner := buildArchive(nil, nil)
	outer := buildArchive([]specEntry{regularSpec("a", []byte("payload"))}, nil)
	evil := append(inner, outer...)
	_, err := VerifyBytes(evil, testPolicy())
	if !errors.Is(err, ErrMultipleEOCD) && !errors.Is(err, ErrGap) && !errors.Is(err, ErrBadSignature) {
		t.Fatalf("err = %v, want a multi-EOCD/gap rejection", err)
	}
}

func TestLFHCDHMismatches(t *testing.T) {
	base := regularSpec("a.txt", []byte("consistent content"))
	cases := []struct {
		name string
		mod  func(*specEntry)
		want error
	}{
		{"crc", func(s *specEntry) { s.lfhOnlyCRC = u32p(0xdeadbeef) }, ErrHeaderMismatch},
		{"compressed size", func(s *specEntry) { s.lfhOnlyCompSize = u32p(7) }, ErrHeaderMismatch},
		{"uncompressed size", func(s *specEntry) { s.lfhOnlyUncompSize = u32p(9) }, ErrHeaderMismatch},
		{"method", func(s *specEntry) { s.lfhOnlyMethod = u16p(methodStored) }, ErrHeaderMismatch},
		{"flags", func(s *specEntry) { s.lfhOnlyFlags = u16p(flagUTF8) }, ErrHeaderMismatch},
		{"name", func(s *specEntry) { s.lfhOnlyName = "b.txt" }, ErrHeaderMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := base
			tc.mod(&s)
			arc := buildArchive([]specEntry{s}, nil)
			_, err := VerifyBytes(arc, testPolicy())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCDHLieAboutUncompressedSize(t *testing.T) {
	s := regularSpec("a", []byte("actual payload bytes"))
	s.overrideUncompSize = u32p(99)
	s.lfhOnlyUncompSize = u32p(99)
	_, err := VerifyBytes(buildArchive([]specEntry{s}, nil), testPolicy())
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
}

func TestCRCMismatchOnCorruptedPayload(t *testing.T) {
	specs := []specEntry{{name: "a", content: []byte("store me"), method: methodStored, hostSystem: 3}}
	arc := buildArchive(specs, nil)
	off := lfhFixedSize + len("a") + 2
	arc[off] ^= 0xff
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrCRC) {
		t.Fatalf("err = %v, want ErrCRC", err)
	}
}

func TestDataDescriptorMismatch(t *testing.T) {
	specs := []specEntry{{
		name: "a", content: []byte("descriptor test"),
		method: methodDeflate, dataDescriptor: true, hostSystem: 3,
	}}
	arc := buildArchive(specs, nil)
	ddOff := indexSignature(arc, sigDD, 0)
	if ddOff < 0 {
		t.Fatal("data descriptor signature not found")
	}
	patch32(arc, ddOff+4, 0x12345678)
	_, err := VerifyBytes(arc, testPolicy())
	if !errors.Is(err, ErrBadDescriptor) && !errors.Is(err, ErrCRC) {
		t.Fatalf("err = %v, want ErrBadDescriptor/ErrCRC", err)
	}
}

func TestValidDataDescriptor(t *testing.T) {
	specs := []specEntry{{
		name: "a", content: []byte(strings.Repeat("descriptor-ok ", 50)),
		method: methodDeflate, dataDescriptor: true, hostSystem: 3,
	}}
	if _, err := VerifyBytes(buildArchive(specs, nil), testPolicy()); err != nil {
		t.Fatalf("data descriptor archive rejected: %v", err)
	}
}

func TestDuplicateNames(t *testing.T) {
	cases := [][]specEntry{
		{regularSpec("a.txt", []byte("1")), regularSpec("a.txt", []byte("2"))},
		{regularSpec("Foo.txt", nil), regularSpec("foo.txt", nil)},
		{regularSpec("dir/a", nil), regularSpec(`dir\a`, nil)},
		{regularSpec("X/Y", nil), regularSpec(`x\y`, nil)},
	}
	for i, specs := range cases {
		_, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
		if !errors.Is(err, ErrDuplicateName) {
			t.Fatalf("case %d: err = %v, want ErrDuplicateName", i, err)
		}
	}
}

func TestZipSlipAndUnsafeNames(t *testing.T) {
	names := []string{
		"../evil.txt",
		"../../etc/passwd",
		"foo/../../evil",
		`..\windows\system32\evil`,
		"/etc/passwd",
		`C:\windows\system32`,
		"c:/windows",
		"//server/share",
		"a/../b/../../c",
	}
	for _, name := range names {
		specs := []specEntry{{name: name, content: []byte("x"), method: methodStored, hostSystem: 3}}
		_, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
		if !errors.Is(err, ErrUnsafeName) {
			t.Fatalf("name %q: err = %v, want ErrUnsafeName", name, err)
		}
	}
	for _, name := range []string{"a/./b", "foo..bar", "dir/file.ext", "a/b/"} {
		specs := []specEntry{{name: name, content: []byte("x"), method: methodStored, hostSystem: 3}}
		if _, err := VerifyBytes(buildArchive(specs, nil), testPolicy()); err != nil {
			t.Fatalf("benign name %q rejected: %v", name, err)
		}
	}
}

func TestUnsafeModes(t *testing.T) {
	cases := []struct {
		name string
		mode uint32
		want error
	}{
		{"symlink", unixAttrs(fs.ModeSymlink | 0o777), ErrUnsafeMode},
		{"suid", uint32(0o4755) << 16, ErrUnsafeMode},
		{"sgid", uint32(0o2755) << 16, ErrUnsafeMode},
		{"fifo", unixAttrs(fs.ModeNamedPipe | 0o644), ErrUnsafeMode},
		{"device", unixAttrs(fs.ModeDevice | 0o644), ErrUnsafeMode},
		{"socket", unixAttrs(fs.ModeSocket | 0o644), ErrUnsafeMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specs := []specEntry{{
				name: "x", content: []byte("link-target"),
				method: methodStored, hostSystem: 3, externalAttrs: tc.mode,
			}}
			_, err := VerifyBytes(buildArchive(specs, nil), testPolicy())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestModePolicyRelaxed(t *testing.T) {
	p := testPolicy()
	p.RejectSymlink = false
	specs := []specEntry{{
		name: "link", content: []byte("target"),
		method: methodStored, hostSystem: 3,
		externalAttrs: unixAttrs(fs.ModeSymlink | 0o777),
	}}
	rep, err := VerifyBytes(buildArchive(specs, nil), p)
	if err != nil {
		t.Fatalf("relaxed symlink policy rejected: %v", err)
	}
	if rep.Entries[0].UnixMode&fs.ModeSymlink == 0 {
		t.Fatalf("symlink mode not surfaced: %#o", rep.Entries[0].UnixMode)
	}
}
