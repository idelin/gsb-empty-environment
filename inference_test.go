package netsec

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func freezeTwoTensorModel(t *testing.T) (*Model, *fakeFS, *fakeNode, *fakeNode) {
	t.Helper()
	dataA := []byte{10, 11, 12, 13}
	dataB := []byte{20, 21, 22, 23}
	filesystem := newFakeFS()
	root := filesystem.addRoot("model")
	fileA := filesystem.addFile(root, "a.bin", dataA)
	fileB := filesystem.addFile(root, "b.bin", dataB)
	a := initForRange("a", "a.bin", dataA, 0, 4, Uint8, []int64{4})
	b := initForRange("b", "b.bin", dataB, 0, 4, Uint8, []int64{4})
	model, err := Freeze(Spec{
		Root:         "model",
		GraphDigest:  SumDigest([]byte("graph")),
		Initializers: []Initializer{a, b},
		FileSystem:   filesystem,
	})
	if err != nil {
		t.Fatalf("Freeze: %v", err)
	}
	t.Cleanup(func() { _ = model.Close() })
	return model, filesystem, fileA, fileB
}

func TestPathReplacementAndRootRecreationReadFrozenHandles(t *testing.T) {
	model, filesystem, _, _ := freezeTwoTensorModel(t)
	sentinel := []byte("HOST-ROOT-SENTINEL")
	root := filesystem.roots["model"]

	// Replace both the individual weight path and the model directory path
	// after freezing. The model must keep using its open handles.
	filesystem.addFile(root, "a.bin", sentinel)
	delete(filesystem.roots, "model")
	recreated := filesystem.addRoot("model")
	filesystem.addFile(recreated, "a.bin", sentinel)
	filesystem.addFile(recreated, "b.bin", sentinel)

	var callbackData []byte
	if err := model.ReadTensor("a", func(p []byte) error {
		callbackData = append([]byte(nil), p...)
		return nil
	}); err != nil {
		t.Fatalf("ReadTensor after replacement: %v", err)
	}
	if !bytes.Equal(callbackData, []byte{10, 11, 12, 13}) {
		t.Fatalf("callback got %v, want original frozen bytes", callbackData)
	}
	if bytes.Contains(callbackData, sentinel) {
		t.Fatal("host sentinel reached tensor callback")
	}

	got, err := model.Tensor("b")
	if err != nil {
		t.Fatalf("Tensor(b) after root recreation: %v", err)
	}
	if !bytes.Equal(got, []byte{20, 21, 22, 23}) {
		t.Fatalf("Tensor(b) = %v, want original frozen bytes", got)
	}
}

func TestTruncationInvalidatesWholeModel(t *testing.T) {
	model, filesystem, _, fileB := freezeTwoTensorModel(t)
	filesystem.truncate(fileB, 2)

	// Reading a different, untouched tensor must still fail because the
	// whole instance is invalid.
	_, err := model.Tensor("a")
	if !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor(a) after truncation = %v, want ErrModelInvalid", err)
	}
	if _, err := model.Tensor("b"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor(b) after truncation = %v, want ErrModelInvalid", err)
	}
	if model.Invalid() == nil {
		t.Fatal("model did not retain its invalid state")
	}
}

func TestShortReadInvalidatesModel(t *testing.T) {
	model, _, fileA, _ := freezeTwoTensorModel(t)
	fileA.shortRead = true

	_, err := model.Tensor("a")
	if !errors.Is(err, ErrModelInvalid) || !errors.Is(err, ErrShortRead) {
		t.Fatalf("Tensor(a) short read error = %v, want model invalid + short read", err)
	}
	if _, err := model.Tensor("b"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor(b) after short read = %v, want ErrModelInvalid", err)
	}
}

func TestManifestMismatchInvalidatesModel(t *testing.T) {
	model, _, _, _ := freezeTwoTensorModel(t)
	manifest := model.Manifest()
	manifest.GraphDigest[0] ^= 0xff

	if err := model.VerifyManifest(manifest); !errors.Is(err, ErrManifestMismatch) {
		t.Fatalf("VerifyManifest(tampered) = %v, want ErrManifestMismatch", err)
	}
	if _, err := model.Tensor("a"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor after manifest mismatch = %v, want ErrModelInvalid", err)
	}
}

func TestVerifyDetectsInPlaceContentChange(t *testing.T) {
	model, filesystem, _, fileB := freezeTwoTensorModel(t)
	// Simulate a hostile filesystem that changes bytes while keeping the
	// reported identity metadata unchanged.
	fileB.data[0] ^= 0xff

	if err := model.Verify(); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("Verify after content change = %v, want ErrDigestMismatch", err)
	}
	if _, err := model.Tensor("a"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor after Verify failure = %v, want ErrModelInvalid", err)
	}
	_ = filesystem
}

func TestInPlaceWriteToOtherFileInvalidates(t *testing.T) {
	model, filesystem, _, fileB := freezeTwoTensorModel(t)
	filesystem.write(fileB, []byte{0xee, 0xee, 0xee, 0xee})

	if _, err := model.Tensor("a"); !errors.Is(err, ErrModelInvalid) {
		t.Fatalf("Tensor(a) after other file write = %v, want ErrModelInvalid", err)
	}
}

func TestTensorBytesArePrivateCopies(t *testing.T) {
	model, _, _, _ := freezeTwoTensorModel(t)
	first, err := model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor(a): %v", err)
	}
	first[0] = 0xee
	second, err := model.Tensor("a")
	if err != nil {
		t.Fatalf("Tensor(a) second read: %v", err)
	}
	if second[0] != 10 {
		t.Fatalf("frozen buffer was mutated through returned slice: %v", second)
	}
}

func TestUnknownTensorAndClose(t *testing.T) {
	model, _, _, _ := freezeTwoTensorModel(t)
	if _, err := model.Tensor("missing"); !errors.Is(err, ErrUnknownTensor) {
		t.Fatalf("unknown tensor error = %v", err)
	}
	if err := model.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := model.Tensor("a"); !errors.Is(err, ErrModelClosed) {
		t.Fatalf("Tensor after Close = %v, want ErrModelClosed", err)
	}
}

func TestSentinelNeverReachesCallbackAfterReplacement(t *testing.T) {
	model, filesystem, _, _ := freezeTwoTensorModel(t)
	sentinel := []byte("HOST-ROOT-SENTINEL")
	filesystem.addFile(filesystem.roots["model"], "a.bin", sentinel)

	var seen []byte
	err := model.ReadTensor("a", func(p []byte) error {
		seen = append(seen, p...)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTensor: %v", err)
	}
	if strings.Contains(string(seen), "HOST-ROOT-SENTINEL") {
		t.Fatalf("host sentinel reached tensor callback: %q", seen)
	}
}
