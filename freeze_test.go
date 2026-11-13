package netsec

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func initForRange(id string, location string, data []byte, offset, length int64, dtype DType, shape []int64) Initializer {
	var digest Digest
	if offset >= 0 && length >= 0 && offset <= int64(len(data)) && length <= int64(len(data))-offset {
		digest = SumDigest(data[offset : offset+length])
	}
	return Initializer{
		TensorID: id,
		Location: location,
		Offset:   offset,
		Length:   length,
		SHA256:   digest,
		Dtype:    dtype,
		Shape:    shape,
	}
}

func TestValidateLocation(t *testing.T) {
	valid := []string{
		"weights.bin",
		"layers/block-0/weight.bin",
		"模型/权重.bin",
		"a.b/c_d-e",
	}
	for _, location := range valid {
		if err := ValidateLocation(location); err != nil {
			t.Fatalf("ValidateLocation(%q) = %v, want nil", location, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"./weights.bin",
		"layers/../weights.bin",
		"layers/./weights.bin",
		"layers//weights.bin",
		"layers/",
		"/etc/passwd",
		"//server/share",
		`layers\weights.bin`,
		"weights\x00.bin",
		"%2e%2e/weights.bin",
		"layers/%2Fweights.bin",
		"C:/weights.bin",
		string([]byte{0xff, 0xfe}),
	}
	for _, location := range invalid {
		if err := ValidateLocation(location); !errors.Is(err, ErrInvalidLocation) {
			t.Fatalf("ValidateLocation(%q) = %v, want ErrInvalidLocation", location, err)
		}
	}
}

func TestTensorByteLengthAndInitializerValidation(t *testing.T) {
	length, err := TensorByteLength(Float32, []int64{2, 3})
	if err != nil || length != 24 {
		t.Fatalf("TensorByteLength = %d, %v; want 24, nil", length, err)
	}
	if _, err := TensorByteLength(Float32, []int64{-1}); !errors.Is(err, ErrInvalidInitializer) {
		t.Fatalf("negative shape error = %v", err)
	}
	if _, err := TensorByteLength(Float32, []int64{1 << 61, 8}); !errors.Is(err, ErrInvalidInitializer) {
		t.Fatalf("overflowing shape error = %v", err)
	}
	if _, err := TensorByteLength(DType("nonsense"), []int64{1}); !errors.Is(err, ErrUnsupportedDType) {
		t.Fatalf("unknown dtype error = %v", err)
	}

	digest := SumDigest([]byte{1, 2, 3, 4})
	valid := Initializer{
		TensorID: "t",
		Location: "w.bin",
		Length:   4,
		SHA256:   digest,
		Dtype:    Float32,
		Shape:    []int64{1},
	}
	if _, err := normalizeInitializer(valid); err != nil {
		t.Fatalf("valid initializer rejected: %v", err)
	}

	wrongLength := valid
	wrongLength.Shape = []int64{2}
	if _, err := normalizeInitializer(wrongLength); !errors.Is(err, ErrInvalidTensorLength) {
		t.Fatalf("wrong length error = %v", err)
	}

	overflow := valid
	overflow.Offset = 1<<63 - 1
	overflow.Length = 1
	overflow.Shape = []int64{0}
	if _, err := normalizeInitializer(overflow); !errors.Is(err, ErrInvalidInitializer) {
		t.Fatalf("offset overflow error = %v", err)
	}
}

func TestFreezeAndReadFromFrozenHandles(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	filesystem.addFile(root, "weights.bin", data)

	a := initForRange("a", "weights.bin", data, 0, 4, Float32, []int64{1})
	b := initForRange("b", "weights.bin", data, 4, 8, Float32, []int64{2})
	graphDigest := SumDigest([]byte("graph"))
	model, err := Freeze(Spec{
		Root:         "model",
		GraphDigest:  graphDigest,
		Initializers: []Initializer{a, b},
		FileSystem:   filesystem,
	})
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	defer model.Close()

	got, err := model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor(a): %v", err)
	}
	if string(got) != string(data[:4]) {
		t.Fatalf("Tensor(a) = %v, want %v", got, data[:4])
	}

	var callbackBytes []byte
	if err := model.ReadTensor("b", func(p []byte) error {
		callbackBytes = append([]byte(nil), p...)
		return nil
	}); err != nil {
		t.Fatalf("ReadTensor(b): %v", err)
	}
	if string(callbackBytes) != string(data[4:]) {
		t.Fatalf("callback bytes = %v, want %v", callbackBytes, data[4:])
	}

	manifest := model.Manifest()
	if manifest.GraphDigest != graphDigest {
		t.Fatalf("manifest graph digest = %s, want %s", manifest.GraphDigest, graphDigest)
	}
	if len(manifest.Files) != 1 || len(manifest.Tensors) != 2 {
		t.Fatalf("manifest files/tensors = %d/%d, want 1/2", len(manifest.Files), len(manifest.Tensors))
	}
	if manifest.Files[0].Location != "weights.bin" {
		t.Fatalf("manifest location = %q", manifest.Files[0].Location)
	}
	if manifest.Files[0].Identity.Inode == 0 {
		t.Fatal("manifest did not record file identity")
	}
	if err := model.VerifyManifest(manifest); err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
}

func TestSymlinkIsRejectedBeforeOpen(t *testing.T) {
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	host := filesystem.newInode(0o600)
	host.data = []byte("HOST-ROOT-SENTINEL")
	filesystem.addSymlink(root, "creds.bin", host)

	a := initForRange("a", "creds.bin", host.data, 0, int64(len(host.data)), Uint8, []int64{int64(len(host.data))})
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("Freeze symlink error = %v, want ErrSymlink", err)
	}
	if strings.Contains(err.Error(), "HOST-ROOT-SENTINEL") {
		t.Fatal("error exposed host sentinel bytes")
	}
}

func TestDirectorySwapBetweenLstatAndOpenIsRejected(t *testing.T) {
	sentinel := []byte("HOST-ROOT-SENTINEL")
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	weights := filesystem.addDir(root, "weights")
	filesystem.addFile(weights, "w.bin", []byte{1, 2, 3, 4})

	hostDir := filesystem.newInode(fs.ModeDir | 0o755)
	hostDir.children = make(map[string]*fakeNode)
	filesystem.addFile(hostDir, "w.bin", sentinel)

	filesystem.beforeOpenDir = func(dir *fakeDir, name string) {
		if name == "weights" {
			filesystem.addSymlink(root, "weights", hostDir)
		}
	}

	a := Initializer{
		TensorID: "a",
		Location: "weights/w.bin",
		Length:   4,
		SHA256:   SumDigest([]byte{1, 2, 3, 4}),
		Dtype:    Uint8,
		Shape:    []int64{4},
	}
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrChangedDuringOpen) && !errors.Is(err, ErrSymlink) {
		t.Fatalf("Freeze directory swap error = %v, want change/symlink error", err)
	}
}

func TestFileSwapBetweenLstatAndOpenIsRejected(t *testing.T) {
	sentinel := []byte("HOST-ROOT-SENTINEL")
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	weights := filesystem.addDir(root, "weights")
	filesystem.addFile(weights, "w.bin", []byte{1, 2, 3, 4})
	host := filesystem.newInode(0o600)
	host.data = sentinel

	filesystem.beforeOpenFile = func(dir *fakeDir, name string) {
		if name == "w.bin" {
			filesystem.addSymlink(weights, "w.bin", host)
		}
	}

	a := Initializer{
		TensorID: "a",
		Location: "weights/w.bin",
		Length:   4,
		SHA256:   SumDigest([]byte{1, 2, 3, 4}),
		Dtype:    Uint8,
		Shape:    []int64{4},
	}
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrChangedDuringOpen) && !errors.Is(err, ErrSymlink) {
		t.Fatalf("Freeze file swap error = %v, want change/symlink error", err)
	}
}

func TestHardlinkSwapBetweenLstatAndOpenIsRejected(t *testing.T) {
	sentinel := []byte("HOST-ROOT-SENTINEL")
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	filesystem.addFile(root, "w.bin", []byte{1, 2, 3, 4})
	host := filesystem.newInode(0o600)
	host.data = sentinel

	filesystem.beforeOpenFile = func(dir *fakeDir, name string) {
		if name == "w.bin" {
			filesystem.addHardlink(root, "w.bin", host)
		}
	}

	a := Initializer{
		TensorID: "a",
		Location: "w.bin",
		Length:   4,
		SHA256:   SumDigest([]byte{1, 2, 3, 4}),
		Dtype:    Uint8,
		Shape:    []int64{4},
	}
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrChangedDuringOpen) && !errors.Is(err, ErrHardlink) {
		t.Fatalf("Freeze hardlink swap error = %v, want change/hardlink error", err)
	}
}

func TestHardlinkToHostCredentialIsRejected(t *testing.T) {
	sentinel := []byte("HOST-ROOT-SENTINEL")
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	hostDir := filesystem.addDir(root, "host")
	host := filesystem.addFile(hostDir, "credential", sentinel)
	filesystem.addHardlink(root, "weights.bin", host)

	a := Initializer{
		TensorID: "a",
		Location: "weights.bin",
		Length:   int64(len(sentinel)),
		SHA256:   SumDigest(sentinel),
		Dtype:    Uint8,
		Shape:    []int64{int64(len(sentinel))},
	}
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrHardlink) {
		t.Fatalf("Freeze hardlink error = %v, want ErrHardlink", err)
	}
}

func TestDigestAndRangeValidation(t *testing.T) {
	data := []byte{1, 2, 3, 4}
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	filesystem.addFile(root, "w.bin", data)

	badDigest := initForRange("a", "w.bin", data, 0, 4, Uint8, []int64{4})
	badDigest.SHA256 = SumDigest([]byte("wrong"))
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{badDigest}, FileSystem: filesystem})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("digest mismatch error = %v", err)
	}

	outOfRange := initForRange("a", "w.bin", data, 2, 4, Uint8, []int64{4})
	_, err = Freeze(Spec{Root: "model", Initializers: []Initializer{outOfRange}, FileSystem: filesystem})
	if !errors.Is(err, ErrInvalidInitializer) {
		t.Fatalf("out-of-range error = %v", err)
	}
}

func TestOverlapRules(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	filesystem.addFile(root, "w.bin", data)

	a := initForRange("a", "w.bin", data, 0, 4, Uint8, []int64{4})
	b := initForRange("b", "w.bin", data, 2, 4, Uint8, []int64{4})
	b.Shared = true
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a, b}, FileSystem: filesystem})
	if !errors.Is(err, ErrOverlappingRanges) {
		t.Fatalf("different-id overlap error = %v, want ErrOverlappingRanges", err)
	}

	sharedA := a
	sharedA.Shared = true
	sharedA2 := sharedA
	model, err := Freeze(Spec{Root: "model", Initializers: []Initializer{sharedA, sharedA2}, FileSystem: filesystem})
	if err != nil {
		t.Fatalf("exact shared alias rejected: %v", err)
	}
	defer model.Close()
	if got := model.Manifest().Tensors; len(got) != 1 {
		t.Fatalf("shared alias produced %d tensors, want 1", len(got))
	}

	unshared := sharedA
	unshared.Shared = false
	_, err = Freeze(Spec{Root: "model", Initializers: []Initializer{unshared, unshared}, FileSystem: filesystem})
	if !errors.Is(err, ErrDuplicateTensorID) {
		t.Fatalf("unshared duplicate error = %v, want ErrDuplicateTensorID", err)
	}
}

func TestNonRegularFinalEntryRejected(t *testing.T) {
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	filesystem.addDir(root, "weights.bin")

	a := Initializer{
		TensorID: "a",
		Location: "weights.bin",
		Length:   0,
		SHA256:   SumDigest(nil),
		Dtype:    Uint8,
		Shape:    []int64{0},
	}
	_, err := Freeze(Spec{Root: "model", Initializers: []Initializer{a}, FileSystem: filesystem})
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("non-regular error = %v, want ErrNotRegular", err)
	}
}
