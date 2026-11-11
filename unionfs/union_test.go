package unionfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// tarBuilder constructs an in-memory tar archive and records, for every
// regular file, the byte range its content occupies in the final stream.
type tarBuilder struct {
	buf    bytes.Buffer
	ranges map[string]tarRange
}

type tarRange struct {
	offset int64
	size   int64
}

func (b *tarBuilder) add(name string, typ byte, content, link string) {
	if b.ranges == nil {
		b.ranges = map[string]tarRange{}
	}
	hdr := &tar.Header{
		Name:     name,
		Typeflag: typ,
		Mode:     0o644,
		Linkname: link,
	}
	switch typ {
	case tar.TypeDir:
		hdr.Mode = 0o755
	case tar.TypeSymlink, tar.TypeLink:
		hdr.Mode = 0o777
	}
	if typ == tar.TypeReg || typ == tar.TypeRegA {
		hdr.Size = int64(len(content))
	}
	if typ == tar.TypeDir && !strings.HasSuffix(name, "/") {
		hdr.Name = name + "/"
	}
	w := tar.NewWriter(&b.buf)
	if err := w.WriteHeader(hdr); err != nil {
		panic(err)
	}
	if (typ == tar.TypeReg || typ == tar.TypeRegA) && len(content) > 0 {
		off := int64(b.buf.Len())
		if _, err := w.Write([]byte(content)); err != nil {
			panic(err)
		}
		b.ranges[name] = tarRange{offset: off, size: int64(len(content))}
	}
	if err := w.Flush(); err != nil {
		panic(err)
	}
}

func (b *tarBuilder) bytes() []byte {
	return append([]byte(nil), b.buf.Bytes()...)
}

// countingReaderAt records every ReadAt call. Indexing a layer must only read
// 512-byte header blocks and never touch file content ranges.
type countingReaderAt struct {
	data    []byte
	mu      sync.Mutex
	calls   int
	ranges  []byteRange
	content map[string]tarRange
}

type byteRange struct{ off, end int64 }

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.mu.Lock()
	c.calls++
	c.ranges = append(c.ranges, byteRange{off, off + int64(len(p))})
	c.mu.Unlock()
	if off >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(p, c.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (c *countingReaderAt) touchesContent() []byteRange {
	c.mu.Lock()
	defer c.mu.Unlock()
	var bad []byteRange
	for _, r := range c.ranges {
		for _, cr := range c.content {
			if r.off < cr.offset+cr.size && r.end > cr.offset {
				bad = append(bad, r)
				break
			}
		}
	}
	return bad
}

func newCountingLayer(t *testing.T, b *tarBuilder) (*Layer, *countingReaderAt) {
	t.Helper()
	data := b.bytes()
	cr := &countingReaderAt{data: data, content: b.ranges}
	return NewLayer(cr, int64(len(data))), cr
}

func mustLayer(t *testing.T, b *tarBuilder) *Layer {
	t.Helper()
	layer, _ := newCountingLayer(t, b)
	return layer
}

func entryNames(entries []fs.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLazyIndexingAndSparseReads(t *testing.T) {
	var b tarBuilder
	contentA := strings.Repeat("A", 4096)
	contentB := strings.Repeat("B", 1300)
	contentC := strings.Repeat("C", 17)
	b.add("dir/", tar.TypeDir, "", "")
	b.add("dir/a.bin", tar.TypeReg, contentA, "")
	b.add("dir/b.bin", tar.TypeReg, contentB, "")
	b.add("c.txt", tar.TypeReg, contentC, "")

	layer, counter := newCountingLayer(t, &b)
	u := New(layer)

	if err := u.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	if bad := counter.touchesContent(); len(bad) != 0 {
		t.Fatalf("indexing read file content ranges: %v", bad)
	}

	f, err := u.Open("dir/a.bin")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	ra := f.(io.ReaderAt)

	checks := []struct {
		off  int64
		n    int
		want byte
	}{
		{0, 1, 'A'},
		{100, 16, 'A'},
		{4095, 1, 'A'},
		{3000, 256, 'A'},
	}
	for _, c := range checks {
		buf := make([]byte, c.n)
		n, err := ra.ReadAt(buf, c.off)
		if err != nil {
			t.Fatalf("ReadAt(%d,%d): %v", c.off, c.n, err)
		}
		if n != c.n || buf[0] != c.want {
			t.Fatalf("ReadAt(%d,%d) = %d, %q", c.off, c.n, n, buf[:1])
		}
	}

	buf := make([]byte, 8)
	if _, err := ra.ReadAt(buf, 4090); !errors.Is(err, io.EOF) {
		t.Fatalf("past-end ReadAt err = %v, want EOF", err)
	}

	seeker := f.(io.Seeker)
	if _, err := seeker.Seek(2048, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	rest := make([]byte, 16)
	if _, err := io.ReadFull(f.(io.Reader), rest); err != nil {
		t.Fatalf("read after seek: %v", err)
	}
	if !bytes.Equal(rest, []byte(strings.Repeat("A", 16))) {
		t.Fatalf("sought data mismatch: %q", rest)
	}

	if got, err := u.ReadFile("dir/b.bin"); err != nil || string(got) != contentB {
		t.Fatalf("b.bin: %v len=%d", err, len(got))
	}
	if got, err := u.ReadFile("c.txt"); err != nil || string(got) != contentC {
		t.Fatalf("c.txt: %v len=%d", err, len(got))
	}

	// All content reads must stay within one of the recorded content ranges.
	raA := b.ranges["dir/a.bin"]
	raB := b.ranges["dir/b.bin"]
	raC := b.ranges["c.txt"]
	counter.mu.Lock()
	for _, r := range counter.ranges {
		inA := r.off >= raA.offset && r.end <= raA.offset+raA.size
		inB := r.off >= raB.offset && r.end <= raB.offset+raB.size
		inC := r.off >= raC.offset && r.end <= raC.offset+raC.size
		headerOnly := r.end-r.off == blockSize
		if !inA && !inB && !inC && !headerOnly {
			t.Errorf("ReadAt range %+v outside every file/block boundary", r)
		}
	}
	counter.mu.Unlock()
}

// tarEntry is one archive member used to build a shuffled tar in memory.
type tarEntry struct {
	name    string
	typ     byte
	content string
	link    string
}

func buildTar(t *testing.T, entries []tarEntry, rng *rand.Rand) ([]byte, map[string]tarRange) {
	t.Helper()
	order := make([]int, len(entries))
	for i := range order {
		order[i] = i
	}
	if rng != nil {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	}
	var b tarBuilder
	for _, idx := range order {
		e := entries[idx]
		b.add(e.name, e.typ, e.content, e.link)
	}
	return b.bytes(), b.ranges
}

func shuffledLayer(t *testing.T, entries []tarEntry, rng *rand.Rand) *Layer {
	t.Helper()
	data, ranges := buildTar(t, entries, rng)
	cr := &countingReaderAt{data: data, content: ranges}
	return NewLayer(cr, int64(len(data)))
}

func TestShuffledLayersAndWhiteouts(t *testing.T) {
	baseEntries := []tarEntry{
		{"app/", tar.TypeDir, "", ""},
		{"app/keep.txt", tar.TypeReg, "base-keep", ""},
		{"app/remove.txt", tar.TypeReg, "base-remove", ""},
		{"app/sub/", tar.TypeDir, "", ""},
		{"app/sub/low.txt", tar.TypeReg, "low", ""},
		{"app/sub/mid.txt", tar.TypeReg, "mid-low", ""},
		{"app/hl.txt", tar.TypeReg, "hardlink-source", ""},
	}
	midEntries := []tarEntry{
		{"app/", tar.TypeDir, "", ""},
		{"app/remove.txt", tar.TypeReg, "mid-reborn", ""},
		{"app/.wh.keep.txt", tar.TypeReg, "", ""},
		{"app/hl-copy", tar.TypeLink, "", "app/hl.txt"},
	}
	topEntries := []tarEntry{
		{"app/", tar.TypeDir, "", ""},
		{"app/.wh.remove.txt", tar.TypeReg, "", ""},
		{"app/sub/.wh..wh..opq", tar.TypeReg, "", ""},
		{"app/sub/top.txt", tar.TypeReg, "top", ""},
		{"app/.wh.gone.txt", tar.TypeReg, "", ""},
		{"app/gone.txt", tar.TypeReg, "same-layer-wins", ""},
	}

	for _, seed := range []int64{1, 2, 3, 7, 42} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			base := shuffledLayer(t, baseEntries, rng)
			mid := shuffledLayer(t, midEntries, rng)
			top := shuffledLayer(t, topEntries, rng)

			stack := []*Layer{base, mid, top}
			rng.Shuffle(3, func(i, j int) { stack[i], stack[j] = stack[j], stack[i] })
			// Restore logical low->high order after exercising construction;
			// New() receives the ordered stack by contract.
			stack = []*Layer{base, mid, top}
			u := New(stack...)

			if _, err := u.Stat("app/keep.txt"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("keep.txt stat err = %v, want NotExist", err)
			}
			if _, err := u.Stat("app/remove.txt"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("remove.txt stat err = %v, want NotExist", err)
			}
			for _, p := range []string{"app/sub/low.txt", "app/sub/mid.txt"} {
				if _, err := u.Stat(p); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("%s stat err = %v, want NotExist", p, err)
				}
			}
			if data, err := u.ReadFile("app/sub/top.txt"); err != nil || string(data) != "top" {
				t.Fatalf("sub/top.txt = %q, %v", data, err)
			}
			if data, err := u.ReadFile("app/gone.txt"); err != nil || string(data) != "same-layer-wins" {
				t.Fatalf("gone.txt = %q, %v", data, err)
			}
			if data, err := u.ReadFile("app/hl-copy"); err != nil || string(data) != "hardlink-source" {
				t.Fatalf("hl-copy = %q, %v", data, err)
			}

			entries, err := u.ReadDir("app")
			if err != nil {
				t.Fatalf("readdir app: %v", err)
			}
			if got := entryNames(entries); !equalStrings(got, []string{"gone.txt", "hl-copy", "hl.txt", "sub"}) {
				t.Fatalf("app entries = %v", got)
			}
			subEntries, err := u.ReadDir("app/sub")
			if err != nil {
				t.Fatalf("readdir sub: %v", err)
			}
			if got := entryNames(subEntries); !equalStrings(got, []string{"top.txt"}) {
				t.Fatalf("sub entries = %v", got)
			}
		})
	}
}

func TestDirectoryEnumerationAndImplicitDirs(t *testing.T) {
	low := shuffledLayer(t, []tarEntry{
		{"a/b/c/deep.txt", tar.TypeReg, "deep", ""},
		{"a/b/other.txt", tar.TypeReg, "other", ""},
	}, rand.New(rand.NewSource(11)))
	high := shuffledLayer(t, []tarEntry{
		{"a/b/.wh.other.txt", tar.TypeReg, "", ""},
		{"a/b/new.txt", tar.TypeReg, "new", ""},
		{"root.txt", tar.TypeReg, "root", ""},
	}, rand.New(rand.NewSource(12)))

	u := New(low, high)

	root, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	if got := entryNames(root); !equalStrings(got, []string{"a", "root.txt"}) {
		t.Fatalf("root entries = %v", got)
	}

	a, err := u.ReadDir("a")
	if err != nil {
		t.Fatalf("readdir a: %v", err)
	}
	if got := entryNames(a); !equalStrings(got, []string{"b"}) {
		t.Fatalf("a entries = %v", got)
	}

	bEntries, err := u.ReadDir("a/b")
	if err != nil {
		t.Fatalf("readdir a/b: %v", err)
	}
	if got := entryNames(bEntries); !equalStrings(got, []string{"c", "new.txt"}) {
		t.Fatalf("a/b entries = %v", got)
	}

	cEntries, err := u.ReadDir("a/b/c")
	if err != nil {
		t.Fatalf("readdir c: %v", err)
	}
	if got := entryNames(cEntries); !equalStrings(got, []string{"deep.txt"}) {
		t.Fatalf("c entries = %v", got)
	}

	// Whiteouted lower file is gone; synthesized directories are stat-able.
	if _, err := u.Stat("a/b/other.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("other.txt err = %v", err)
	}
	info, err := u.Stat("a/b")
	if err != nil || !info.IsDir() {
		t.Fatalf("stat a/b = %v, %v", info, err)
	}
	if data, err := u.ReadFile("root.txt"); err != nil || string(data) != "root" {
		t.Fatalf("root.txt = %q, %v", data, err)
	}
}

func TestEscapingLinksAndHardlinkSafety(t *testing.T) {
	low := shuffledLayer(t, []tarEntry{
		{"safe/secret.txt", tar.TypeReg, "secret", ""},
		{"safe/inner/", tar.TypeDir, "", ""},
		{"safe/inner/file.txt", tar.TypeReg, "inner-file", ""},
		{"linkdir/", tar.TypeDir, "", ""},
		{"linkdir/evil", tar.TypeSymlink, "", "../../safe"},
		{"loop-a", tar.TypeSymlink, "", "loop-b"},
		{"loop-b", tar.TypeSymlink, "", "loop-a"},
	}, rand.New(rand.NewSource(5)))

	high := shuffledLayer(t, []tarEntry{
		{"abs-evil", tar.TypeSymlink, "", "/safe/secret.txt"},
		{"good-link", tar.TypeSymlink, "", "/safe/inner/file.txt"},
		{"rel-good", tar.TypeSymlink, "", "safe/secret.txt"},
		// Hardlink targeting a symlink: must fail closed.
		{"hl-to-symlink", tar.TypeLink, "", "linkdir/evil"},
		// Hardlink escaping the root: must fail closed.
		{"hl-escape", tar.TypeLink, "", "../outside"},
		// Hardlink to a file hidden by this same layer's whiteout.
		{".wh.hidden-target", tar.TypeReg, "", ""},
		{"hl-hidden", tar.TypeLink, "", "hidden-target"},
		// Hardlink to a valid visible object in the lower layer.
		{"hl-ok", tar.TypeLink, "", "safe/secret.txt"},
	}, rand.New(rand.NewSource(6)))

	u := New(low, high)

	// Symlink traversal that climbs out of the union root is rejected.
	if _, err := u.Open("linkdir/evil/secret.txt"); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("evil link err = %v, want ErrEscapesRoot", err)
	}
	if _, err := u.Stat("linkdir/evil/inner/file.txt"); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("evil stat err = %v, want ErrEscapesRoot", err)
	}

	// Absolute links resolve against the union root, not the host filesystem.
	if data, err := u.ReadFile("abs-evil"); err != nil || string(data) != "secret" {
		t.Fatalf("abs-evil = %q, %v", data, err)
	}
	if data, err := u.ReadFile("good-link"); err != nil || string(data) != "inner-file" {
		t.Fatalf("good-link = %q, %v", data, err)
	}
	if data, err := u.ReadFile("rel-good"); err != nil || string(data) != "secret" {
		t.Fatalf("rel-good = %q, %v", data, err)
	}

	// Symlink loops surface an invalid-path error rather than hanging.
	if _, err := u.Open("loop-a"); !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("loop err = %v, want Invalid", err)
	}

	// Unsafe hardlinks remain enumerable in listings but fail closed on
	// Open/Stat.
	for _, p := range []string{"hl-to-symlink", "hl-escape", "hl-hidden"} {
		if _, err := u.Stat(p); err == nil {
			t.Fatalf("stat %s unexpectedly succeeded", p)
		}
		if _, err := u.Open(p); err == nil {
			t.Fatalf("open %s unexpectedly succeeded", p)
		}
	}

	// The safe hardlink binds to the lower-layer regular file object.
	if data, err := u.ReadFile("hl-ok"); err != nil || string(data) != "secret" {
		t.Fatalf("hl-ok = %q, %v", data, err)
	}

	// Unsafe hardlink nodes still appear in directory listings.
	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := entryNames(entries)
	for _, want := range []string{"hl-to-symlink", "hl-escape", "hl-hidden", "hl-ok"} {
		if !contains(names, want) {
			t.Fatalf("root listing %v missing %s", names, want)
		}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

var (
	_ fs.FS         = (*Union)(nil)
	_ fs.ReadDirFS  = (*Union)(nil)
	_ fs.StatFS     = (*Union)(nil)
	_ fs.ReadFileFS = (*Union)(nil)
)

func TestPAXLongPathsAndLargeSize(t *testing.T) {
	// Standard tar.Writer emits PAX extension headers for long names and
	// large sizes; the lazy scanner must follow them without reading content.
	longName := "deeply/nested/directory/" + strings.Repeat("very-long-name-", 20) + ".bin"
	content := strings.Repeat("Z", 300)
	var b tarBuilder
	b.add(longName, tar.TypeReg, content, "")

	layer, counter := newCountingLayer(t, &b)
	u := New(layer)
	if err := u.build(); err != nil {
		t.Fatalf("build: %v", err)
	}
	if bad := counter.touchesContent(); len(bad) != 0 {
		t.Fatalf("indexing touched PAX file content: %v", bad)
	}
	got, err := u.ReadFile(longName)
	if err != nil || string(got) != content {
		t.Fatalf("PAX long-name read = len %d, %v", len(got), err)
	}
	info, err := u.Stat(longName)
	if err != nil || info.Size() != int64(len(content)) {
		t.Fatalf("PAX stat = %v, %v", info, err)
	}
}

func TestSameLayerMarkersOrderIndependent(t *testing.T) {
	// Whiteout appears before and after a same-named real entry; both orders
	// must keep the real entry.
	for _, seed := range []int64{1, 99} {
		rng := rand.New(rand.NewSource(seed))
		layer := shuffledLayer(t, []tarEntry{
			{"data/.wh.file", tar.TypeReg, "", ""},
			{"data/file", tar.TypeReg, "alive", ""},
			{"data/other", tar.TypeReg, "x", ""},
			{"data/.wh.other", tar.TypeReg, "", ""},
		}, rng)
		u := New(layer)
		got, err := u.ReadFile("data/file")
		if err != nil || string(got) != "alive" {
			t.Fatalf("seed %d: data/file = %q, %v", seed, got, err)
		}
		// Same-layer whiteout does not hide a same-layer entry.
		if _, err := u.Stat("data/other"); err != nil {
			t.Fatalf("seed %d: same-layer other err = %v", seed, err)
		}
		entries, err := u.ReadDir("data")
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if got := entryNames(entries); !equalStrings(got, []string{"file", "other"}) {
			t.Fatalf("seed %d: data entries = %v", seed, got)
		}
	}
}

func TestLowerLayerWhiteoutAcrossLayers(t *testing.T) {
	low := shuffledLayer(t, []tarEntry{
		{"data/", tar.TypeDir, "", ""},
		{"data/old.txt", tar.TypeReg, "old", ""},
		{"data/keep.txt", tar.TypeReg, "keep", ""},
	}, rand.New(rand.NewSource(7)))
	high := shuffledLayer(t, []tarEntry{
		{"data/.wh.old.txt", tar.TypeReg, "", ""},
		{"data/new.txt", tar.TypeReg, "new", ""},
	}, rand.New(rand.NewSource(8)))
	u := New(low, high)
	if _, err := u.Stat("data/old.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("whiteouted old.txt err = %v", err)
	}
	entries, err := u.ReadDir("data")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if got := entryNames(entries); !equalStrings(got, []string{"keep.txt", "new.txt"}) {
		t.Fatalf("data entries = %v", got)
	}
}

func TestPathValidationAndRoot(t *testing.T) {
	layer := mustLayer(t, &tarBuilder{})
	u := New(layer)
	for _, bad := range []string{"../escape", "a/../../escape", "/../escape", "a/./b/../../.."} {
		if _, err := u.Open(bad); !errors.Is(err, ErrEscapesRoot) {
			t.Fatalf("Open(%q) err = %v, want ErrEscapesRoot", bad, err)
		}
	}
	root, err := u.Open(".")
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer root.Close()
	info, err := root.Stat()
	if err != nil || !info.IsDir() {
		t.Fatalf("root stat = %v, %v", info, err)
	}
	entries, err := u.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty root readdir = %v, %v", entries, err)
	}
}

func TestLayeredOverwritesAndSymlinkDirs(t *testing.T) {
	low := shuffledLayer(t, []tarEntry{
		{"etc/", tar.TypeDir, "", ""},
		{"etc/conf", tar.TypeReg, "v1", ""},
		{"versions/", tar.TypeDir, "", ""},
		{"versions/v2/", tar.TypeDir, "", ""},
		{"versions/v2/conf", tar.TypeReg, "v2", ""},
		{"etc/current", tar.TypeSymlink, "", "../versions/v2"},
	}, rand.New(rand.NewSource(3)))
	high := shuffledLayer(t, []tarEntry{
		{"etc/conf", tar.TypeReg, "v3", ""},
	}, rand.New(rand.NewSource(4)))
	u := New(low, high)

	if got, err := u.ReadFile("etc/conf"); err != nil || string(got) != "v3" {
		t.Fatalf("overwritten conf = %q, %v", got, err)
	}
	if got, err := u.ReadFile("etc/current/conf"); err != nil || string(got) != "v2" {
		t.Fatalf("symlink-dir read = %q, %v", got, err)
	}
	entries, err := u.ReadDir("etc/current")
	if err != nil {
		t.Fatalf("readdir via symlink: %v", err)
	}
	if got := entryNames(entries); !equalStrings(got, []string{"conf"}) {
		t.Fatalf("symlink dir entries = %v", got)
	}
}

func TestHardlinkOrderIndependentAndDangling(t *testing.T) {
	// The hardlink is emitted before its target in the archive; binding must
	// still succeed because index resolution ignores entry order.
	for _, seed := range []int64{21, 22, 23} {
		rng := rand.New(rand.NewSource(seed))
		layer := shuffledLayer(t, []tarEntry{
			{"link-to-target", tar.TypeLink, "", "dir/target.bin"},
			{"dir/", tar.TypeDir, "", ""},
			{"dir/target.bin", tar.TypeReg, "payload", ""},
		}, rng)
		u := New(layer)
		if got, err := u.ReadFile("link-to-target"); err != nil || string(got) != "payload" {
			t.Fatalf("seed %d: hardlink = %q, %v", seed, got, err)
		}
	}

	// A hardlink to a target hidden by a higher-layer whiteout fails closed.
	low := shuffledLayer(t, []tarEntry{
		{"target", tar.TypeReg, "data", ""},
		{"link", tar.TypeLink, "", "target"},
	}, rand.New(rand.NewSource(31)))
	high := shuffledLayer(t, []tarEntry{
		{".wh.target", tar.TypeReg, "", ""},
	}, rand.New(rand.NewSource(32)))
	u := New(low, high)
	if _, err := u.Open("link"); err == nil {
		t.Fatalf("hardlink to whiteouted target should fail closed")
	}
}

func TestRootOpaqueMarker(t *testing.T) {
	low := shuffledLayer(t, []tarEntry{
		{"old.txt", tar.TypeReg, "old", ""},
		{"old-dir/", tar.TypeDir, "", ""},
		{"old-dir/file", tar.TypeReg, "x", ""},
	}, rand.New(rand.NewSource(41)))
	high := shuffledLayer(t, []tarEntry{
		{".wh..wh..opq", tar.TypeReg, "", ""},
		{"new.txt", tar.TypeReg, "new", ""},
	}, rand.New(rand.NewSource(42)))
	u := New(low, high)
	if _, err := u.Stat("old.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("root opaque: old.txt err = %v", err)
	}
	if _, err := u.Stat("old-dir/file"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("root opaque: old-dir/file err = %v", err)
	}
	if got, err := u.ReadFile("new.txt"); err != nil || string(got) != "new" {
		t.Fatalf("root opaque: new.txt = %q, %v", got, err)
	}
	entries, err := u.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if got := entryNames(entries); !equalStrings(got, []string{"new.txt"}) {
		t.Fatalf("root entries = %v", got)
	}
}

func TestUnsafeArchiveEntryPathRejected(t *testing.T) {
	// Hand-craft a tar block with a traversal name (tar.Writer refuses these)
	// so the scanner's own path validation is exercised.
	block := make([]byte, blockSize)
	copy(block[0:8], []byte("../evil\x00"))
	copy(block[100:108], []byte("0000644\x00"))
	copy(block[108:116], []byte("0000000\x00"))
	copy(block[116:124], []byte("0000000\x00"))
	copy(block[124:136], []byte("00000000000\x00"))
	copy(block[136:148], []byte("00000000000\x00"))
	block[156] = tar.TypeReg
	copy(block[257:263], []byte("ustar\x00"))
	copy(block[263:265], []byte("00"))
	data := append(append([]byte{}, block...), make([]byte, blockSize)...)
	cr := &countingReaderAt{data: data, content: map[string]tarRange{}}
	layer := NewLayer(cr, int64(len(data)))
	u := New(layer)
	if err := u.build(); err == nil {
		t.Fatalf("expected indexing error for traversal entry")
	}
}

func TestConcurrentLazyAccess(t *testing.T) {
	var b tarBuilder
	for i := 0; i < 20; i++ {
		b.add(fmt.Sprintf("f%02d.txt", i), tar.TypeReg, strings.Repeat("x", 100+i), "")
	}
	layer, _ := newCountingLayer(t, &b)
	u := New(layer)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("f%02d.txt", id%20)
			data, err := u.ReadFile(name)
			if err != nil || len(data) != 100+(id%20) {
				t.Errorf("concurrent read %s: len=%d err=%v", name, len(data), err)
			}
			if _, err := u.ReadDir("."); err != nil {
				t.Errorf("concurrent readdir: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestSparseReadAtExactRanges(t *testing.T) {
	// Three files of distinct sizes; every ReadAt of file N must land inside
	// file N's own recorded content range (no cross-file reads).
	var b tarBuilder
	files := map[string]string{
		"a": strings.Repeat("a", 100),
		"b": strings.Repeat("b", 1000),
		"c": strings.Repeat("c", 5),
	}
	for name, body := range files {
		b.add(name, tar.TypeReg, body, "")
	}
	layer, counter := newCountingLayer(t, &b)
	u := New(layer)

	for name, body := range files {
		f, err := u.Open(name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		ra := f.(io.ReaderAt)
		buf := make([]byte, 3)
		if _, err := ra.ReadAt(buf, int64(len(body)-3)); err != nil {
			t.Fatalf("ReadAt %s: %v", name, err)
		}
		f.Close()
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()
	for _, r := range counter.ranges {
		matched := false
		for name := range files {
			cr := b.ranges[name]
			if r.off >= cr.offset && r.end <= cr.offset+cr.size {
				matched = true
				break
			}
		}
		// 512-byte header/padding blocks are also legitimate indexing I/O.
		if !matched && r.end-r.off != blockSize {
			t.Errorf("range %+v matches no file content and is not a header block", r)
		}
	}
}

func TestTruncatedAndCorruptArchive(t *testing.T) {
	cases := map[string][]byte{
		"short header": bytes.Repeat([]byte{'a'}, 100),
		"garbage block": func() []byte {
			b := bytes.Repeat([]byte{'x'}, blockSize)
			return append(b, make([]byte, blockSize)...)
		}(),
		"empty": {},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			cr := &countingReaderAt{data: data, content: map[string]tarRange{}}
			u := New(NewLayer(cr, int64(len(data))))
			if err := u.build(); err != nil && name == "empty" {
				t.Fatalf("empty archive should index cleanly: %v", err)
			} else if err == nil && name != "empty" {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}
