package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// sparseReaderAt is deliberately virtual: it stores only written spans and
// fills unwritten bytes with zeroes. Tests can make the logical file much
// larger than memory while still presenting an io.ReaderAt.
type sparseReaderAt struct {
	mu         sync.RWMutex
	size       int64
	generation uint64
	chunks     map[int64][]byte
	deadAt     int64
}

func newSparseReaderAt(size int64) *sparseReaderAt {
	return &sparseReaderAt{
		size:   size,
		chunks: make(map[int64][]byte),
		deadAt: -1,
	}
}

func (s *sparseReaderAt) Size() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.size
}

func (s *sparseReaderAt) Generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.generation
}

func (s *sparseReaderAt) WriteAt(p []byte, off int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks[off] = append([]byte(nil), p...)
	if end := off + int64(len(p)); end > s.size {
		s.size = end
	}
}

func (s *sparseReaderAt) ReadAt(p []byte, off int64) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= s.size {
		return 0, io.EOF
	}
	if s.deadAt >= 0 && off >= s.deadAt {
		return 0, nil
	}
	n := len(p)
	if int64(n) > s.size-off {
		n = int(s.size - off)
	}
	if s.deadAt > off && s.deadAt < off+int64(n) {
		n = int(s.deadAt - off)
	}
	clear(p[:n])
	for chunkOff, chunk := range s.chunks {
		lo := maxInt64(off, chunkOff)
		hi := minInt64(off+int64(n), chunkOff+int64(len(chunk)))
		if lo >= hi {
			continue
		}
		copy(p[lo-off:hi-off], chunk[lo-chunkOff:hi-chunkOff])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (s *sparseReaderAt) bumpGeneration() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
}

func (s *sparseReaderAt) truncate(size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = size
	s.generation++
	for off := range s.chunks {
		if off >= size {
			delete(s.chunks, off)
		}
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func containerBytes(t *testing.T, metadata string, data []byte) []byte {
	t.Helper()
	file := make([]byte, 8+len(metadata)+len(data))
	binary.LittleEndian.PutUint64(file[:8], uint64(len(metadata)))
	copy(file[8:], metadata)
	copy(file[8+len(metadata):], data)
	return file
}

func sparseContainer(t *testing.T, metadata string, data []byte) *sparseReaderAt {
	t.Helper()
	file := containerBytes(t, metadata, data)
	s := newSparseReaderAt(int64(len(file)))
	s.WriteAt(file, 0)
	return s
}

func mustParse(t *testing.T, metadata string, data []byte) *Model {
	t.Helper()
	s := sparseContainer(t, metadata, data)
	m, err := Parse(s, s.Size())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func tensorJSON(dtype string, shape string, begin, end uint64) string {
	return fmt.Sprintf(`{"dtype":%q,"shape":%s,"data_offsets":[%d,%d]}`, dtype, shape, begin, end)
}

func TestValidContainerAndCanonicalManifest(t *testing.T) {
	metadataA := `{
		"z": ` + tensorJSON("U8", "[3]", 8, 11) + `,
		"a": ` + tensorJSON("F32", "[2]", 0, 8) + `,
		"empty": ` + tensorJSON("F64", "[2,0]", 11, 11) + `
	}`
	metadataB := `{"empty":` + tensorJSON("F64", "[2,0]", 11, 11) +
		`,"a":` + tensorJSON("F32", "[2]", 0, 8) +
		`,"z":` + tensorJSON("U8", "[3]", 8, 11) + `}`
	data := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	m := mustParse(t, metadataA, data)
	defer m.Close()

	gotNames := m.Names()
	wantNames := []string{"a", "empty", "z"}
	if fmt.Sprint(gotNames) != fmt.Sprint(wantNames) {
		t.Fatalf("names = %v, want %v", gotNames, wantNames)
	}
	for _, name := range wantNames {
		if _, ok := m.Tensors[name]; !ok {
			t.Fatalf("tensor %q missing", name)
		}
	}
	if m.Tensors["empty"].ByteLength() != 0 {
		t.Fatalf("empty tensor byte length = %d", m.Tensors["empty"].ByteLength())
	}

	got := make([]byte, 8)
	if n, err := m.Tensors["a"].ReadAt(got, 0); n != 8 || err != nil {
		t.Fatalf("read a: n=%d err=%v", n, err)
	}
	if !bytes.Equal(got, data[:8]) {
		t.Fatalf("a data = %v, want %v", got, data[:8])
	}
	if n, err := m.Tensors["z"].Data.ReadAt(got[:3], 0); n != 3 || err != nil {
		t.Fatalf("read z: n=%d err=%v", n, err)
	}
	if !bytes.Equal(got[:3], data[8:]) {
		t.Fatalf("z data = %v, want %v", got[:3], data[8:])
	}

	canonical := `{"a":{"dtype":"F32","shape":[2],"data_offsets":[0,8]},` +
		`"empty":{"dtype":"F64","shape":[2,0],"data_offsets":[11,11]},` +
		`"z":{"dtype":"U8","shape":[3],"data_offsets":[8,11]}}`
	if string(m.CanonicalMetadata()) != canonical {
		t.Fatalf("canonical metadata:\n got %s\nwant %s", m.CanonicalMetadata(), canonical)
	}
	wantManifest := sha256.Sum256([]byte(canonical))
	if m.Manifest != wantManifest {
		t.Fatalf("manifest = %x, want %x", m.Manifest, wantManifest)
	}
	if m.Tensors["a"].Manifest() != wantManifest {
		t.Fatalf("tensor manifest is not bound to model manifest")
	}

	m2 := mustParse(t, metadataB, data)
	defer m2.Close()
	if m2.Manifest != m.Manifest {
		t.Fatalf("reordered metadata produced different manifest: %x != %x", m2.Manifest, m.Manifest)
	}
}

func TestInvalidMetadataAndRanges(t *testing.T) {
	validA := tensorJSON("U8", "[1]", 0, 1)
	tests := []struct {
		name string
		meta string
		data []byte
		want error
	}{
		{
			name: "duplicate top-level key",
			meta: `{"a":` + validA + `,"a":` + validA + `}`,
			data: []byte{0},
			want: ErrDuplicateKey,
		},
		{
			name: "duplicate nested key",
			meta: `{"a":{"dtype":"U8","dtype":"U8","shape":[1],"data_offsets":[0,1]}}`,
			data: []byte{0},
			want: ErrDuplicateKey,
		},
		{
			name: "float dimension",
			meta: `{"a":` + tensorJSON("U8", "[1.0]", 0, 1) + `}`,
			data: []byte{0},
			want: ErrInvalidNumber,
		},
		{
			name: "exponent dimension",
			meta: `{"a":` + tensorJSON("U8", "[1e0]", 0, 1) + `}`,
			data: []byte{0},
			want: ErrInvalidNumber,
		},
		{
			name: "uint64 overflow dimension",
			meta: `{"a":` + tensorJSON("U8", "[18446744073709551616]", 0, 0) + `}`,
			want: ErrInvalidNumber,
		},
		{
			name: "negative dimension",
			meta: `{"a":` + tensorJSON("U8", "[-1]", 0, 0) + `}`,
			want: ErrInvalidNumber,
		},
		{
			name: "unknown dtype",
			meta: `{"a":` + tensorJSON("BF16", "[1]", 0, 1) + `}`,
			data: []byte{0},
			want: ErrInvalidDType,
		},
		{
			name: "too few dimensions",
			meta: `{"a":` + tensorJSON("U8", "[]", 0, 0) + `}`,
			want: ErrInvalidShape,
		},
		{
			name: "too many dimensions",
			meta: `{"a":` + tensorJSON("U8", "[1,1,1,1,1,1,1,1,1]", 0, 0) + `}`,
			want: ErrInvalidShape,
		},
		{
			name: "element multiplication overflow",
			meta: `{"a":` + tensorJSON("U8", "[18446744073709551615,2]", 0, 0) + `}`,
			want: ErrInvalidOffsets,
		},
		{
			name: "byte multiplication overflow",
			meta: `{"a":` + tensorJSON("F64", "[2305843009213693952]", 0, 0) + `}`,
			want: ErrInvalidOffsets,
		},
		{
			name: "zero dimension requires empty range",
			meta: `{"a":` + tensorJSON("U8", "[4,0]", 0, 1) + `}`,
			data: []byte{0},
			want: ErrInvalidOffsets,
		},
		{
			name: "range length mismatch",
			meta: `{"a":` + tensorJSON("F32", "[2]", 0, 7) + `}`,
			data: make([]byte, 7),
			want: ErrInvalidOffsets,
		},
		{
			name: "begin greater than end",
			meta: `{"a":` + tensorJSON("U8", "[1]", 2, 1) + `}`,
			data: make([]byte, 2),
			want: ErrInvalidOffsets,
		},
		{
			name: "range outside data region",
			meta: `{"a":` + tensorJSON("U8", "[2]", 0, 2) + `}`,
			data: []byte{0},
			want: ErrInvalidOffsets,
		},
		{
			name: "partial overlap",
			meta: `{"a":` + tensorJSON("U8", "[4]", 0, 4) +
				`,"b":` + tensorJSON("U8", "[4]", 2, 6) + `}`,
			data: make([]byte, 6),
			want: ErrOverlap,
		},
		{
			name: "identical overlap",
			meta: `{"a":` + tensorJSON("U8", "[4]", 0, 4) +
				`,"b":` + tensorJSON("U8", "[4]", 0, 4) + `}`,
			data: make([]byte, 4),
			want: ErrOverlap,
		},
		{
			name: "tensor value not object",
			meta: `{"a":[]}`,
			want: ErrInvalidContainer,
		},
		{
			name: "extra tensor field",
			meta: `{"a":{"dtype":"U8","shape":[1],"data_offsets":[0,1],"x":1}}`,
			data: []byte{0},
			want: ErrInvalidContainer,
		},
		{
			name: "root not object",
			meta: `[]`,
			want: ErrInvalidContainer,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := sparseContainer(t, tc.meta, tc.data)
			m, err := Parse(s, s.Size())
			if err == nil {
				t.Fatalf("Parse succeeded with model %#v", m)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tc.want)
			}
		})
	}
}

func TestNilReaderAt(t *testing.T) {
	var nilReader *sparseReaderAt
	if _, err := Parse(nilReader, 0); !errors.Is(err, ErrInvalidContainer) {
		t.Fatalf("typed nil error = %v, want ErrInvalidContainer", err)
	}
}

func TestZeroDimensionWithHugeOtherDimension(t *testing.T) {
	meta := `{"a":` + tensorJSON("F64", "[18446744073709551615,0]", 3, 3) + `}`
	m := mustParse(t, meta, make([]byte, 3))
	defer m.Close()
	if got := m.Tensors["a"].ByteLength(); got != 0 {
		t.Fatalf("byte length = %d, want 0", got)
	}
}

func TestMetadataCannotConsumeData(t *testing.T) {
	meta := `{"a":` + tensorJSON("U8", "[1]", 0, 1) + `}`
	raw := containerBytes(t, meta, []byte{0x42, 0, 0, 0})

	binary.LittleEndian.PutUint64(raw[:8], uint64(len(meta)+4))
	s := newSparseReaderAt(int64(len(raw)))
	s.WriteAt(raw, 0)
	if _, err := Parse(s, s.Size()); !errors.Is(err, ErrInvalidContainer) {
		t.Fatalf("metadata/data overlap error = %v, want ErrInvalidContainer", err)
	}

	huge := make([]byte, 16)
	binary.LittleEndian.PutUint64(huge[:8], ^uint64(0))
	s = newSparseReaderAt(int64(len(huge)))
	s.WriteAt(huge, 0)
	if _, err := Parse(s, s.Size()); !errors.Is(err, ErrInvalidContainer) {
		t.Fatalf("wrapping metadata length error = %v, want ErrInvalidContainer", err)
	}
}

func TestShortReads(t *testing.T) {
	t.Run("short header", func(t *testing.T) {
		s := newSparseReaderAt(7)
		s.WriteAt(make([]byte, 7), 0)
		if _, err := Parse(s, s.Size()); !errors.Is(err, ErrShortRead) {
			t.Fatalf("error = %v, want ErrShortRead", err)
		}
	})

	t.Run("short metadata read", func(t *testing.T) {
		hdr := make([]byte, 8)
		binary.LittleEndian.PutUint64(hdr, 32)
		s := newSparseReaderAt(40)
		s.WriteAt(hdr, 0)
		s.deadAt = 8
		if _, err := Parse(s, s.Size()); !errors.Is(err, ErrShortRead) {
			t.Fatalf("error = %v, want ErrShortRead", err)
		}
	})

	t.Run("short tensor data read", func(t *testing.T) {
		meta := `{"a":` + tensorJSON("U8", "[4]", 0, 4) + `}`
		s := sparseContainer(t, meta, make([]byte, 4))
		m, err := Parse(s, s.Size())
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()
		s.deadAt = int64(8 + len(meta) + 2)
		buf := make([]byte, 4)
		n, err := m.Tensors["a"].ReadAt(buf, 0)
		if n != 2 || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("read: n=%d err=%v, want n=2 ErrUnexpectedEOF", n, err)
		}
	})
}

func TestGenerationChangesAfterParse(t *testing.T) {
	meta := `{"a":` + tensorJSON("U8", "[4]", 0, 4) + `}`
	s := sparseContainer(t, meta, []byte{1, 2, 3, 4})
	m, err := Parse(s, s.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	s.bumpGeneration()
	if _, err := m.Tensors["a"].ReadAt(make([]byte, 4), 0); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("generation change error = %v, want ErrFileChanged", err)
	}
}

func TestParseFileHandleIsGuarded(t *testing.T) {
	meta := `{"a":` + tensorJSON("U8", "[4]", 0, 4) + `}`
	dir := t.TempDir()
	path := filepath.Join(dir, "model.bin")
	if err := os.WriteFile(path, containerBytes(t, meta, []byte{1, 2, 3, 4}), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(f, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Tensors["a"].ReadAt(make([]byte, 4), 0); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("truncated handle error = %v, want ErrFileChanged", err)
	}
}

func TestFileReplacementAndTruncation(t *testing.T) {
	meta := `{"a":` + tensorJSON("U8", "[4]", 0, 4) + `}`
	raw := containerBytes(t, meta, []byte{1, 2, 3, 4})

	t.Run("replacement", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "model.bin")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()

		replacement := filepath.Join(dir, "replacement.bin")
		if err := os.WriteFile(replacement, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Tensors["a"].ReadAt(make([]byte, 4), 0); !errors.Is(err, ErrFileChanged) {
			t.Fatalf("replacement error = %v, want ErrFileChanged", err)
		}
	})

	t.Run("truncation", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "model.bin")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer m.Close()

		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Tensors["a"].ReadAt(make([]byte, 4), 0); !errors.Is(err, ErrFileChanged) {
			t.Fatalf("truncation error = %v, want ErrFileChanged", err)
		}
	})

	t.Run("close", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "model.bin")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Tensors["a"].ReadAt(make([]byte, 4), 0); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed read error = %v, want ErrClosed", err)
		}
	})
}

func TestOccupancyReference(t *testing.T) {
	meta := `{"a":` + tensorJSON("U8", "[3]", 0, 3) +
		`,"b":` + tensorJSON("I32", "[2]", 3, 11) +
		`,"c":` + tensorJSON("F64", "[1]", 11, 19) +
		`,"empty":` + tensorJSON("F32", "[0]", 19, 19) + `}`
	data := make([]byte, 19)
	m := mustParse(t, meta, data)
	defer m.Close()

	owner := make(map[uint64]string)
	for name, tensor := range m.Tensors {
		begin, end := tensor.DataOffsets[0], tensor.DataOffsets[1]
		if begin > end || end > uint64(len(data)) {
			t.Fatalf("%s has out-of-range offsets [%d,%d)", name, begin, end)
		}
		for off := begin; off < end; off++ {
			if previous, exists := owner[off]; exists {
				t.Fatalf("byte %d is owned by both %q and %q", off, previous, name)
			}
			owner[off] = name
		}
	}
	if len(owner) != len(data) {
		t.Fatalf("covered bytes = %d, want %d", len(owner), len(data))
	}
}
