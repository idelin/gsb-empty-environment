package safeunpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestGoArchiveTarPAXCompatibility(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	longName := strings.Repeat("long/", 40) + "file.txt"
	payload := []byte("pax payload")
	if err := tw.WriteHeader(&tar.Header{
		Name:     longName,
		Mode:     0o644,
		Size:     int64(len(payload)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	longLink := strings.Repeat("link/", 40) + "target"
	if err := tw.WriteHeader(&tar.Header{
		Name:     "a/symlink",
		Mode:     0o777,
		Linkname: longLink,
		Typeflag: tar.TypeSymlink,
		Format:   tar.FormatPAX,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := UnpackTARBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("PAX TAR from archive/tar rejected: %v", err)
	}
	file, ok := result.Lookup(longName)
	if !ok || string(file.Data) != string(payload) {
		t.Fatalf("long PAX path not materialized: %#v", file)
	}
	link, ok := result.Lookup("a/symlink")
	if !ok || link.LinkTarget != longLink {
		t.Fatalf("PAX linkpath override not applied: %#v", link)
	}
}

func TestTarGzipAutoDetection(t *testing.T) {
	raw := makeTar(t, tarEntry{name: "file.txt", data: []byte("hello")})
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := UnpackArchiveBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("legal tar.gz rejected: %v", err)
	}
	if result.Format != FormatTARGzip {
		t.Fatalf("tar.gz not auto-detected: %s", result.Format)
	}
	file, ok := result.Lookup("file.txt")
	if !ok || string(file.Data) != "hello" {
		t.Fatalf("tar.gz file missing: %#v", file)
	}
}

type tarEntry struct {
	name       string
	typeflag   byte
	link       string
	data       []byte
	mode       int64
	headerSize *int64
}

func tarOctal(value int64, width int) []byte {
	field := make([]byte, width)
	text := strconv.FormatInt(value, 8)
	for len(text) < width-1 {
		text = "0" + text
	}
	copy(field, text)
	field[width-1] = 0
	return field
}

func tarChecksumField(value int64) []byte {
	text := strconv.FormatInt(value, 8)
	for len(text) < 6 {
		text = "0" + text
	}
	field := []byte(text + "\x00 ")
	return field
}

func makeTarEntry(t *testing.T, e tarEntry) []byte {
	t.Helper()
	header := make([]byte, tarBlockSize)
	name := e.name
	if len(name) > 100 {
		split := -1
		for i := len(name) - 1; i >= 0; i-- {
			if name[i] == '/' && i <= 155 && len(name)-i-1 <= 100 {
				split = i
				break
			}
		}
		if split < 0 {
			t.Fatalf("test USTAR name cannot be split: %q", e.name)
		}
		copy(header[345:500], name[:split])
		name = name[split+1:]
	}
	if len(e.link) > 100 {
		t.Fatalf("test USTAR link too long: %q", e.link)
	}
	copy(header[0:100], name)
	mode := e.mode
	if mode == 0 {
		mode = 0o644
	}
	copy(header[100:108], tarOctal(mode, 8))
	copy(header[108:116], tarOctal(0, 8))
	copy(header[116:124], tarOctal(0, 8))
	size := int64(len(e.data))
	if e.headerSize != nil {
		size = *e.headerSize
	}
	copy(header[124:136], tarOctal(size, 12))
	copy(header[136:148], tarOctal(0, 12))
	copy(header[148:156], []byte{'0', '0', '0', '0', '0', '0', 0, ' '})
	header[156] = e.typeflag
	if e.typeflag == 0 {
		header[156] = '0'
	}
	copy(header[157:257], e.link)
	copy(header[257:263], []byte("ustar\x00"))
	copy(header[263:265], []byte("00"))
	copy(header[329:337], tarOctal(0, 8))
	copy(header[337:345], tarOctal(0, 8))

	var sum int64
	for i, b := range header {
		if i >= 148 && i < 156 {
			continue
		}
		sum += int64(b)
	}
	copy(header[148:156], tarChecksumField(sum))

	out := append([]byte(nil), header...)
	out = append(out, e.data...)
	pad := (tarBlockSize - len(e.data)%tarBlockSize) % tarBlockSize
	if pad > 0 {
		out = append(out, make([]byte, pad)...)
	}
	return out
}

func makeTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var out []byte
	for _, e := range entries {
		out = append(out, makeTarEntry(t, e)...)
	}
	out = append(out, make([]byte, 2*tarBlockSize)...)
	return out
}

func paxRecord(key, value string) string {
	body := key + "=" + value + "\n"
	for n := len(body) + 2; ; n++ {
		text := strconv.Itoa(n) + " " + body
		if len(text) == n {
			return text
		}
	}
}

func paxData(records ...string) []byte {
	var data []byte
	for _, record := range records {
		data = append(data, record...)
	}
	return data
}

func paxEntry(name string, typeflag byte, data []byte) tarEntry {
	return tarEntry{name: name, typeflag: typeflag, data: data, mode: 0o644}
}

func TestLegalTarTreeAndLinks(t *testing.T) {
	data := makeTar(t,
		tarEntry{name: "dir", typeflag: '5', mode: 0o755},
		tarEntry{name: "hello.txt", data: []byte("hello")},
		tarEntry{name: "dir/link", typeflag: '2', link: "../hello.txt"},
		tarEntry{name: "hard", typeflag: '1', link: "hello.txt"},
	)
	result, err := UnpackTARBytes(data)
	if err != nil {
		t.Fatalf("legal TAR rejected: %v", err)
	}
	if result.Files != 1 || result.Dirs != 1 || result.Symlinks != 1 || result.Hardlinks != 1 {
		t.Fatalf("unexpected counts: %+v", result)
	}
	if result.FileBytes != 5 || result.TotalBytes != 5 {
		t.Fatalf("unexpected byte accounting: total=%d files=%d", result.TotalBytes, result.FileBytes)
	}
	file, ok := result.Lookup("hello.txt")
	if !ok || string(file.Data) != "hello" {
		t.Fatalf("decoded file missing or wrong: %#v", file)
	}
	link, ok := result.Lookup("dir/link")
	if !ok || link.Type != TypeSymlink || link.ResolvedTarget != "hello.txt" {
		t.Fatalf("resolved symlink wrong: %#v", link)
	}
	hard, ok := result.Lookup("hard")
	if !ok || hard.Type != TypeHardlink || hard.ResolvedTarget != "hello.txt" {
		t.Fatalf("resolved hardlink wrong: %#v", hard)
	}
}

func TestPAXLocalPathOverrideIsValidated(t *testing.T) {
	local := paxData(paxRecord("path", "../../etc/passwd"))
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "safe.txt", data: nil},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("PAX path traversal not rejected: %v", err)
	}
}

func TestPAXLocalSizeOverride(t *testing.T) {
	headerSize := int64(0)
	local := paxData(paxRecord("size", "4"))
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "file.txt", data: []byte("data"), headerSize: &headerSize},
	)
	result, err := UnpackTARBytes(data)
	if err != nil {
		t.Fatalf("PAX size override rejected: %v", err)
	}
	file, ok := result.Lookup("file.txt")
	if !ok || string(file.Data) != "data" {
		t.Fatalf("PAX size override did not set decoded size: %#v", file)
	}
}

func TestPAXGlobalSizeInheritance(t *testing.T) {
	zero := int64(0)
	global := paxData(paxRecord("size", "3"))
	data := makeTar(t,
		paxEntry("PaxHeaders/global", 'g', global),
		tarEntry{name: "a.txt", data: []byte("abc"), headerSize: &zero},
		tarEntry{name: "b.txt", data: []byte("def"), headerSize: &zero},
	)
	result, err := UnpackTARBytes(data)
	if err != nil {
		t.Fatalf("global PAX size inheritance rejected: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		file, ok := result.Lookup(name)
		if !ok || string(file.Data) != "abc" && string(file.Data) != "def" {
			t.Fatalf("global PAX size not inherited for %s: %#v", name, file)
		}
	}
}

func TestPAXNegativeSizeRejected(t *testing.T) {
	local := paxData(paxRecord("size", "-1"))
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "file", data: nil},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("negative PAX size not rejected: %v", err)
	}
}

func TestForgedChecksumRejected(t *testing.T) {
	data := makeTar(t, tarEntry{name: "file.txt", data: []byte("x")})
	data[100] ^= 0x01
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("forged checksum not rejected: %v", err)
	}
}

func TestMalformedOctalRejected(t *testing.T) {
	data := makeTar(t, tarEntry{name: "file.txt", data: nil})
	// Replace the size field with an invalid octal digit, then recompute the
	// header checksum so the octal parser is the failing layer under test.
	copy(data[124:136], []byte("00000000008\x00"))
	var sum int64
	for i, b := range data[:tarBlockSize] {
		if i >= 148 && i < 156 {
			continue
		}
		sum += int64(b)
	}
	copy(data[148:156], tarChecksumField(sum))
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed octal field not rejected: %v", err)
	}
}

func TestTwoStageSymlinkParentBlocked(t *testing.T) {
	data := makeTar(t,
		tarEntry{name: "stage", typeflag: '2', link: "target"},
		tarEntry{name: "stage/file.txt", data: []byte("payload")},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrSymlinkTraversal) {
		t.Fatalf("two-stage symlink traversal not rejected: %v", err)
	}
}

func TestHardlinkEscapesAndBadTargetsRejected(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "outside",
			data: makeTar(t,
				tarEntry{name: "file", data: []byte("x")},
				tarEntry{name: "hard", typeflag: '1', link: "../file"},
			),
		},
		{
			name: "missing",
			data: makeTar(t, tarEntry{name: "hard", typeflag: '1', link: "missing"}),
		},
		{
			name: "symlink",
			data: makeTar(t,
				tarEntry{name: "sym", typeflag: '2', link: "file"},
				tarEntry{name: "file", data: []byte("x")},
				tarEntry{name: "hard", typeflag: '1', link: "sym"},
			),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnpackTARBytes(tc.data)
			if !errors.Is(err, ErrHardlink) && !errors.Is(err, ErrLinkEscape) {
				t.Fatalf("hardlink %s not rejected: %v", tc.name, err)
			}
		})
	}
}

func TestDuplicateCaseInsensitiveRejected(t *testing.T) {
	data := makeTar(t,
		tarEntry{name: "Readme.txt", data: []byte("a")},
		tarEntry{name: "readme.txt", data: []byte("b")},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrDuplicatePath) {
		t.Fatalf("case-insensitive duplicate not rejected: %v", err)
	}
}

func TestUnsafePathsRejected(t *testing.T) {
	for _, path := range []string{"/abs", "C:/win", `C:\win`, "../up", "a/../b"} {
		t.Run(path, func(t *testing.T) {
			data := makeTar(t, tarEntry{name: path, data: []byte("x")})
			_, err := UnpackTARBytes(data)
			if !errors.Is(err, ErrUnsafePath) && !errors.Is(err, ErrPathTraversal) {
				t.Fatalf("unsafe path %q not rejected: %v", path, err)
			}
		})
	}
}

func TestResourceLimits(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		data := makeTar(t, tarEntry{name: "big", data: []byte("12345")})
		_, err := UnpackTARBytes(data, WithMaxFileBytes(4))
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("per-file limit not enforced: %v", err)
		}
	})
	t.Run("total", func(t *testing.T) {
		data := makeTar(t,
			tarEntry{name: "a", data: []byte("1234")},
			tarEntry{name: "b", data: []byte("5678")},
		)
		_, err := UnpackTARBytes(data, WithMaxTotalBytes(6))
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("total byte limit not enforced: %v", err)
		}
	})
	t.Run("entries", func(t *testing.T) {
		data := makeTar(t,
			tarEntry{name: "a", data: nil},
			tarEntry{name: "b", data: nil},
		)
		_, err := UnpackTARBytes(data, WithMaxEntries(1))
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("entry limit not enforced: %v", err)
		}
	})
}

func TestGzipBombRatioRejected(t *testing.T) {
	var tarData []byte
	tarData = append(tarData, makeTarEntry(t, tarEntry{name: "zero.bin", data: make([]byte, 4<<20), mode: 0o644})...)
	tarData = append(tarData, make([]byte, 2*tarBlockSize)...)
	var gz bytes.Buffer
	gw := gzip.NewWriter(&gz)
	if _, err := io.Copy(gw, bytes.NewReader(tarData)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := UnpackArchiveBytes(gz.Bytes(),
		WithMaxFileBytes(8<<20),
		WithMaxTotalBytes(8<<20),
		WithMaxCompressionRatio(20),
	)
	if !errors.Is(err, ErrCompressionBomb) {
		t.Fatalf("gzip bomb ratio not rejected: %v", err)
	}
}

func TestMalformedPAXRecordRejected(t *testing.T) {
	local := []byte("99 path=evil\n")
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "safe", data: nil},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed PAX record not rejected: %v", err)
	}
}

func TestTarStringNULInPAXPathRejected(t *testing.T) {
	local := paxData(paxRecord("path", "safe\x00trailing"))
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "safe", data: nil},
	)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("NUL PAX path not rejected: %v", err)
	}
}

func TestUSTARPrefix(t *testing.T) {
	name := strings.Repeat("d", 60) + "/" + strings.Repeat("f", 60)
	data := makeTar(t, tarEntry{name: name, data: []byte("ok")})
	result, err := UnpackTARBytes(data)
	if err != nil {
		t.Fatalf("USTAR prefix path rejected: %v", err)
	}
	file, ok := result.Lookup(name)
	if !ok || string(file.Data) != "ok" {
		t.Fatalf("prefix path missing: %#v", file)
	}
}

func TestEntryCountIncludesPAX(t *testing.T) {
	local := paxData(paxRecord("path", "safe"))
	data := makeTar(t,
		paxEntry("PaxHeaders/file", 'x', local),
		tarEntry{name: "safe", data: nil},
	)
	_, err := UnpackTARBytes(data, WithMaxEntries(1))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("PAX header was not counted as an entry: %v", err)
	}
}

func TestEmptyTar(t *testing.T) {
	data := make([]byte, 2*tarBlockSize)
	result, err := UnpackTARBytes(data)
	if err != nil {
		t.Fatalf("empty TAR rejected: %v", err)
	}
	if result.Files != 0 || result.Root == nil {
		t.Fatalf("empty TAR tree wrong: %#v", result)
	}
}

func TestTruncatedTar(t *testing.T) {
	data := makeTar(t, tarEntry{name: "file", data: []byte("payload")})
	_, err := UnpackTARBytes(data[:tarBlockSize+2])
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated TAR not rejected: %v", err)
	}
}

func TestNonZeroDataAfterTarEndRejected(t *testing.T) {
	data := makeTar(t, tarEntry{name: "file", data: []byte("payload")})
	data = append(data, []byte("hidden")...)
	_, err := UnpackTARBytes(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("non-zero TAR tail not rejected: %v", err)
	}
}

func TestGzipCRCFailureRejected(t *testing.T) {
	raw := makeTar(t, tarEntry{name: "file", data: []byte("hello")})
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	data[len(data)-8] ^= 0xff
	_, err := UnpackArchiveBytes(data)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("invalid gzip CRC not rejected: %v", err)
	}
}
