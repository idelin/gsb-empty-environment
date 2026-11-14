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

// hostWorkspace creates an isolated directory acting as the "host" and
// returns a sentinel file whose contents must never be readable or
// writable through the extraction root.
func hostWorkspace(t *testing.T) (hostDir, sentinelPath string) {
	t.Helper()
	hostDir = t.TempDir()
	sentinelPath = filepath.Join(hostDir, "secret.txt")
	if err := os.WriteFile(sentinelPath, []byte("host-secret"), 0o600); err != nil {
		t.Fatalf("write host sentinel: %v", err)
	}
	return hostDir, sentinelPath
}

func relSentinel(hostDir, sentinelPath string) string {
	rel, err := filepath.Rel(hostDir, sentinelPath)
	if err != nil {
		panic(err)
	}
	return rel
}

// hostEscapePrefix builds enough "../" steps from the eventual destination to
// reach the filesystem root, after which the host path is appended.
func hostEscapePrefix() string {
	return strings.Repeat("../", 8)
}

func TestRejectsHardLinkEscape(t *testing.T) {
	hostDir, sentinelPath := hostWorkspace(t)

	var b rawBuilder
	b.rawEntry("payload.bin", tar.TypeReg, []byte("payload"), "")
	b.rawEntry("evil-link", tar.TypeLink, nil, hostEscapePrefix()+relSentinel(hostDir, sentinelPath))
	b.finish()

	assertReject(t, b.Bytes(), ErrHardLinkTarget)

	data, err := os.ReadFile(sentinelPath)
	if err != nil || string(data) != "host-secret" {
		t.Fatalf("host sentinel altered: data=%q err=%v", data, err)
	}
}

func TestRejectsHardLinkToMissingInArchiveFile(t *testing.T) {
	var b rawBuilder
	b.rawEntry("link-only", tar.TypeLink, nil, "never-created.bin")
	b.finish()
	assertReject(t, b.Bytes(), ErrHardLinkTarget)
}

func TestRejectsSymlinkEscape(t *testing.T) {
	hostDir, sentinelPath := hostWorkspace(t)

	t.Run("parent traversal target", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("escape", tar.TypeSymlink, nil, hostEscapePrefix()+relSentinel(hostDir, sentinelPath))
		b.rawEntry("after", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrLinkEscape)
	})

	t.Run("absolute target", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("abs", tar.TypeSymlink, nil, sentinelPath)
		b.finish()
		assertReject(t, b.Bytes(), ErrLinkEscape)
	})
}

func TestRejectsWriteThroughSymlinkAncestor(t *testing.T) {
	t.Run("directory symlink followed by file write", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("linkdir", tar.TypeSymlink, nil, "realdir")
		b.rawEntry("realdir", tar.TypeDir, nil, "")
		b.rawEntry("linkdir/pwned.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})

	t.Run("symlink chain planted before file", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("a", tar.TypeSymlink, nil, "b")
		b.rawEntry("b", tar.TypeSymlink, nil, "c")
		b.rawEntry("c", tar.TypeDir, nil, "")
		b.rawEntry("a/inside.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})

	t.Run("symlink replacing later file name", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("shadow", tar.TypeSymlink, nil, "target.txt")
		b.rawEntry("shadow", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrDuplicateEntry)
	})

	t.Run("file write under symlink-named directory component", func(t *testing.T) {
		// "link" points at a regular file name; the later entry tries to
		// create a child beneath it, which must never reach the host.
		var b rawBuilder
		b.rawEntry("target.txt", tar.TypeReg, []byte("orig"), "")
		b.rawEntry("link", tar.TypeSymlink, nil, "target.txt")
		b.rawEntry("link/child.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})
}

func TestRejectsAbsolutePathEntry(t *testing.T) {
	t.Run("absolute regular file", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("/tmp/abs-write.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})

	t.Run("parent traversal entry", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("../escape.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})

	t.Run("nested parent traversal entry", func(t *testing.T) {
		var b rawBuilder
		b.rawEntry("a/b/../../../escape.txt", tar.TypeReg, []byte("x"), "")
		b.finish()
		assertReject(t, b.Bytes(), ErrPathEscape)
	})
}

func TestRejectsPAXPathOverrideEscape(t *testing.T) {
	var b rawBuilder
	payload := []byte("pwned")
	next := rawHeader("placeholder", tar.TypeReg, int64(len(payload)), "")
	b.paxEntry(map[string]string{"path": hostEscapePrefix() + "tmp/pax-override.txt"}, next, payload)
	b.finish()
	assertReject(t, b.Bytes(), ErrPathEscape)
}

func TestRejectsPAXLinknameOverrideEscape(t *testing.T) {
	hostDir, sentinelPath := hostWorkspace(t)

	var b rawBuilder
	b.rawEntry("payload.bin", tar.TypeReg, []byte("payload"), "")
	next := rawHeader("evil", tar.TypeLink, 0, "placeholder")
	target := hostEscapePrefix() + relSentinel(hostDir, sentinelPath)
	b.paxEntry(map[string]string{"linkpath": target}, next, nil)
	b.finish()
	assertReject(t, b.Bytes(), ErrHardLinkTarget)
}

func TestAcceptsLongPAXPathInsideRoot(t *testing.T) {
	var b rawBuilder
	payload := []byte("deep-content")
	longName := "deep/" + strings.Repeat("nested/", 20) + "file.txt"
	next := rawHeader("short", tar.TypeReg, int64(len(payload)), "")
	b.paxEntry(map[string]string{"path": longName}, next, payload)
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	if got := readFileString(t, filepath.Join(dest, filepath.FromSlash(longName))); got != string(payload) {
		t.Fatalf("PAX long path content = %q, want %q", got, payload)
	}
}

func TestAcceptsInternalHardLink(t *testing.T) {
	payload := []byte("shared-content")
	var b rawBuilder
	b.rawEntry("dir", tar.TypeDir, nil, "")
	b.rawEntry("dir/original.bin", tar.TypeReg, payload, "")
	b.rawEntry("dir/link.bin", tar.TypeLink, nil, "dir/original.bin")
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	original := filepath.Join(dest, "dir", "original.bin")
	link := filepath.Join(dest, "dir", "link.bin")
	if readFileString(t, link) != string(payload) {
		t.Fatalf("hard link content mismatch")
	}

	originalInfo, err := os.Stat(original)
	if err != nil {
		t.Fatalf("stat original: %v", err)
	}
	linkInfo, err := os.Stat(link)
	if err != nil {
		t.Fatalf("stat link: %v", err)
	}
	if !os.SameFile(originalInfo, linkInfo) {
		t.Fatalf("link.bin is not a hard link of original.bin")
	}

	if err := os.WriteFile(link, []byte("change"), 0o644); err != nil {
		t.Fatalf("write through internal link: %v", err)
	}
	if got := readFileString(t, original); got != "change" {
		t.Fatalf("internal hard link write not visible via original name, got %q", got)
	}
}

func TestAcceptsDeepDirectoryTree(t *testing.T) {
	depth := 30
	dirName := strings.Repeat("d/", depth)
	fileName := dirName + "leaf.txt"
	content := []byte("leaf")

	var b rawBuilder
	b.rawEntry(dirName, tar.TypeDir, nil, "")
	b.rawEntry(fileName, tar.TypeReg, content, "")
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	if got := readFileString(t, filepath.Join(dest, filepath.FromSlash(fileName))); got != string(content) {
		t.Fatalf("deep leaf content = %q", got)
	}
}

func TestRejectsDeviceAndFifoEntries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typeflag byte
	}{
		{"character device", tar.TypeChar},
		{"block device", tar.TypeBlock},
		{"fifo", tar.TypeFifo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b rawBuilder
			b.rawEntry("special", tc.typeflag, nil, "")
			b.finish()
			assertReject(t, b.Bytes(), ErrUnsupportedType)
		})
	}
}

func TestRejectsDuplicateEntries(t *testing.T) {
	var b rawBuilder
	b.rawEntry("same.txt", tar.TypeReg, []byte("one"), "")
	b.rawEntry("same.txt", tar.TypeReg, []byte("two"), "")
	b.finish()
	assertReject(t, b.Bytes(), ErrDuplicateEntry)
}

func TestRejectsTruncatedArchive(t *testing.T) {
	archive := buildTar(t, []*tar.Header{{
		Name:     "file.bin",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     4096,
	}})
	// Drop trailing zero blocks and part of the declared data section.
	truncated := archive[:len(archive)-1200]
	assertReject(t, truncated, ErrMalformedArchive)
}

func TestRejectsEmptyArchive(t *testing.T) {
	assertReject(t, make([]byte, 1024), ErrEmptyArchive)
}

func TestRejectsExistingDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatalf("pre-create dest: %v", err)
	}
	archive := buildTar(t, []*tar.Header{{Name: "a", Typeflag: tar.TypeDir, Mode: 0o755}})
	err := Extract(bytes.NewReader(archive), dest)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("Extract error = %v, want ErrDestinationExists", err)
	}
}

func TestFailureLeavesNoPartialResults(t *testing.T) {
	var b rawBuilder
	b.rawEntry("good-dir", tar.TypeDir, nil, "")
	b.rawEntry("good-dir/good.txt", tar.TypeReg, []byte("ok"), "")
	b.rawEntry("../bad.txt", tar.TypeReg, []byte("evil"), "")
	b.finish()

	dest := filepath.Join(t.TempDir(), "out")
	if err := Extract(bytes.NewReader(b.Bytes()), dest); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("Extract error = %v", err)
	}
	if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination published despite failure: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatalf("read parent: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".safeuntar-staging-") {
			t.Fatalf("staging directory leaked: %q", entry.Name())
		}
	}
}

func TestInternalSymlinkResolvesAndStaysInsideRoot(t *testing.T) {
	var b rawBuilder
	b.rawEntry("target", tar.TypeDir, nil, "")
	b.rawEntry("target/file.txt", tar.TypeReg, []byte("hello"), "")
	b.rawEntry("link", tar.TypeSymlink, nil, "target")
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	resolved, err := filepath.EvalSymlinks(filepath.Join(dest, "link"))
	if err != nil {
		t.Fatalf("resolve internal symlink: %v", err)
	}
	if !isWithin(dest, resolved) {
		t.Fatalf("symlink resolved outside root: %q", resolved)
	}
	if got := readFileString(t, filepath.Join(resolved, "file.txt")); got != "hello" {
		t.Fatalf("content through symlink = %q", got)
	}
}

func TestHostSentinelNeverReachableFromRoot(t *testing.T) {
	hostDir, sentinelPath := hostWorkspace(t)

	var b rawBuilder
	b.rawEntry("normal.txt", tar.TypeReg, []byte("fine"), "")
	b.rawEntry("inner", tar.TypeDir, nil, "")
	b.rawEntry("inner/link", tar.TypeSymlink, nil, hostEscapePrefix()+relSentinel(hostDir, sentinelPath))
	b.finish()

	// The whole archive must be rejected, so no partial tree exists.
	assertReject(t, b.Bytes(), ErrLinkEscape)
}

func isWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func TestSparsePAXFileIsMaterializedConsistently(t *testing.T) {
	// Logical layout of 12 bytes: "AB" + six NULs + "CD" + two NULs.
	// PAX 0.1 map lists the two data fragments; the stream holds only 4
	// physical bytes.
	logicalSize := int64(12)
	dataFragment := []byte("ABCD")

	var b rawBuilder
	records := map[string]string{
		"GNU.sparse.major":     "0",
		"GNU.sparse.minor":     "1",
		"GNU.sparse.numblocks": "2",
		"GNU.sparse.map":       "0,2,8,2",
		"GNU.sparse.size":      "12",
	}
	next := rawHeader("sparse.bin", tar.TypeReg, int64(len(dataFragment)), "")
	b.paxEntry(records, next, dataFragment)
	b.finish()

	dest := extractToTemp(t, b.Bytes())
	path := filepath.Join(dest, "sparse.bin")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat sparse file: %v", err)
	}
	if info.Size() != logicalSize {
		t.Fatalf("sparse file size = %d, want %d", info.Size(), logicalSize)
	}
	want := append([]byte("AB"), make([]byte, 6)...)
	want = append(want, 'C', 'D', 0, 0)
	if got := readFileString(t, path); got != string(want) {
		t.Fatalf("sparse content = %v, want %v", []byte(got), want)
	}
}

func TestRejectsMalformedSparseMap(t *testing.T) {
	t.Run("overlapping intervals", func(t *testing.T) {
		var b rawBuilder
		records := map[string]string{
			"GNU.sparse.major":     "0",
			"GNU.sparse.minor":     "1",
			"GNU.sparse.numblocks": "2",
			"GNU.sparse.map":       "0,4,2,4",
			"GNU.sparse.size":      "12",
		}
		next := rawHeader("sparse.bin", tar.TypeReg, 8, "")
		b.paxEntry(records, next, []byte("ABCDEFGH"))
		b.finish()
		assertReject(t, b.Bytes(), ErrMalformedArchive)
	})

	t.Run("declared size disagrees with header", func(t *testing.T) {
		var b rawBuilder
		records := map[string]string{
			"GNU.sparse.major":     "0",
			"GNU.sparse.minor":     "1",
			"GNU.sparse.numblocks": "1",
			"GNU.sparse.map":       "0,2",
			"GNU.sparse.size":      "2",
			"GNU.sparse.realsize":  "8",
		}
		next := rawHeader("sparse.bin", tar.TypeReg, 2, "")
		b.paxEntry(records, next, []byte("AB"))
		b.finish()
		assertReject(t, b.Bytes(), ErrMalformedArchive)
	})

	t.Run("truncated sparse data section", func(t *testing.T) {
		// The header advertises 4 physical bytes but the stream is cut to
		// 2, so the sparse reader must fail with missing-data.
		var b rawBuilder
		records := map[string]string{
			"GNU.sparse.major":     "0",
			"GNU.sparse.minor":     "1",
			"GNU.sparse.numblocks": "1",
			"GNU.sparse.map":       "0,4",
			"GNU.sparse.size":      "8",
		}
		next := rawHeader("sparse.bin", tar.TypeReg, 4, "")
		b.paxEntry(records, next, []byte("AB"))
		raw := b.Bytes()
		// Locate the 2-byte payload block and truncate after those 2 bytes.
		payloadAt := bytes.LastIndex(raw, []byte("AB\x00"))
		if payloadAt < 0 {
			t.Fatal("payload block not found")
		}
		truncated := raw[:payloadAt+2]
		assertReject(t, truncated, ErrMalformedArchive)
	})
}

func TestRejectsUnreadableSymlinkTarget(t *testing.T) {
	var b rawBuilder
	b.rawEntry("empty-target", tar.TypeSymlink, nil, "")
	b.finish()
	assertReject(t, b.Bytes(), ErrLinkEscape)
}
