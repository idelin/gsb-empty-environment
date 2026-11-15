package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalHMACDetectsUIDChange(t *testing.T) {
	key := testKey()
	state := baseState()
	record := mustSealHMAC(t, state, key)
	state.EVM = record

	result := NewEvaluator(key).Evaluate(state)
	if !result.Allowed() {
		t.Fatalf("valid HMAC denied: %s", result)
	}

	changed := state
	changed.UID++
	changed.EVM = record
	if result := NewEvaluator(key).Evaluate(changed); result.Allowed() {
		t.Fatal("uid change was accepted")
	} else if result.Reason != ReasonHMACMismatch {
		t.Fatalf("uid change reason = %s, want %s", result.Reason, ReasonHMACMismatch)
	}

	transition := NewEvaluator(key).EvaluateTransition(state, changed)
	if transition.Allowed() || transition.Reason != ReasonAttributeMismatch {
		t.Fatalf("uid transition = %+v, want attribute mismatch denial", transition)
	}
}

func TestCopiedEVMIsNotValidForAnotherInode(t *testing.T) {
	key := testKey()
	original := baseState()
	sourcePath, copyPath, sourceInode, copiedInode := createCopy(t)
	original.Path = sourcePath
	original.Inode = sourceInode
	record := mustSealHMAC(t, original, key)
	original.EVM = record

	copied := original
	copied.Path = copyPath
	copied.Inode = copiedInode
	copied.EVM = append([]byte(nil), record...)
	if copied.Inode == original.Inode {
		t.Fatal("copy unexpectedly shares the original inode")
	}

	result := NewEvaluator(key).Evaluate(copied)
	if result.Allowed() {
		t.Fatal("EVM copied to a different inode was accepted")
	}
	if result.Reason != ReasonHMACMismatch {
		t.Fatalf("copy reason = %s, want %s", result.Reason, ReasonHMACMismatch)
	}

	transition := NewEvaluator(key).EvaluateTransition(original, copied)
	if transition.Allowed() || transition.Reason != ReasonInodeMismatch {
		t.Fatalf("copy transition = %+v, want inode mismatch denial", transition)
	}
}

func TestHardLinkWithSameInodeIsAllowed(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "hardlink")
	if err := os.WriteFile(first, []byte("same inode"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(first, second); err != nil {
		t.Fatal(err)
	}

	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("test setup did not create a hard link")
	}

	key := testKey()
	state := baseState()
	state.Path = first
	state.Inode = inodeNumber(t, firstInfo)
	state.EVM = mustSealHMAC(t, state, key)

	if result := NewEvaluator(key).Evaluate(state); !result.Allowed() {
		t.Fatalf("first hard-link path denied: %s", result)
	}

	linked := state
	linked.Path = second
	result := NewEvaluator(key).Evaluate(linked)
	if !result.Allowed() {
		t.Fatalf("hard link with identical inode denied: %s", result)
	}
}

func TestMissingKeyDeniesWithoutIMAFallback(t *testing.T) {
	key := testKey()
	state := baseState()
	state.IMA = []byte("independently-present-ima")
	state.EVM = mustSealHMAC(t, state, key)

	for name, evaluator := range map[string]*Evaluator{
		"nil evaluator":     nil,
		"no keys":           NewEvaluator(nil),
		"nil trusted key":   NewEvaluator(nil, nil),
		"no HMAC key":       NewEvaluator(nil, ed25519.PublicKey(testKey()[:32])),
		"unrelated pub key": NewEvaluator(nil, ed25519.PublicKey(testKey()[:32])),
	} {
		t.Run(name, func(t *testing.T) {
			result := evaluator.Evaluate(state)
			if result.Allowed() {
				t.Fatal("evaluation succeeded without the host HMAC key")
			}
			if result.Reason != ReasonKeyUnavailable {
				t.Fatalf("reason = %s, want %s", result.Reason, ReasonKeyUnavailable)
			}
		})
	}
}

func TestPortableSignatureDoesNotBindInode(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := baseState()
	state.EVM = mustSignPortable(t, state, privateKey)

	evaluator := NewEvaluator(nil, publicKey)
	if result := evaluator.Evaluate(state); !result.Allowed() {
		t.Fatalf("valid portable signature denied: %s", result)
	}

	copied := state
	copied.Path = "/tmp/portable-copy"
	copied.Inode += 4096
	copied.Generation += 3
	if result := evaluator.Evaluate(copied); !result.Allowed() {
		t.Fatalf("portable signature should survive copy: %s", result)
	}

	changed := copied
	changed.UID++
	if result := evaluator.Evaluate(changed); result.Allowed() {
		t.Fatal("portable signature accepted a uid change")
	} else if result.Reason != ReasonSignatureInvalid {
		t.Fatalf("portable uid change reason = %s, want %s", result.Reason, ReasonSignatureInvalid)
	}

	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if result := NewEvaluator(nil, otherPublic).Evaluate(state); result.Allowed() {
		t.Fatal("portable signature accepted an untrusted key")
	} else if result.Reason != ReasonSignatureInvalid {
		t.Fatalf("untrusted signature reason = %s, want %s", result.Reason, ReasonSignatureInvalid)
	}
}

func TestPortableSignatureWithRSAAndECDSA(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	for name, signer := range map[string]crypto.Signer{
		"rsa":   rsaKey,
		"ecdsa": ecdsaKey,
	} {
		t.Run(name, func(t *testing.T) {
			state := baseState()
			state.EVM = mustSignPortable(t, state, signer)
			if result := NewEvaluator(nil, signer.Public()).Evaluate(state); !result.Allowed() {
				t.Fatalf("valid %s portable signature denied: %s", name, result)
			}

			state.GID++
			if result := NewEvaluator(nil, signer.Public()).Evaluate(state); result.Allowed() {
				t.Fatalf("%s signature accepted a gid change", name)
			}
		})
	}
}

func TestUnknownTypeMalformedAndMissingEVM(t *testing.T) {
	key := testKey()
	state := baseState()

	state.EVM = []byte{0x42, 0x01, 0x02}
	if result := NewEvaluator(key).Evaluate(state); result.Allowed() {
		t.Fatal("unknown type byte was accepted")
	} else if result.Reason != ReasonUnknownType {
		t.Fatalf("unknown type reason = %s, want %s", result.Reason, ReasonUnknownType)
	}

	state.EVM = []byte{EVMTypeHMAC, 0x01}
	if result := NewEvaluator(key).Evaluate(state); result.Allowed() {
		t.Fatal("truncated HMAC was accepted")
	} else if result.Reason != ReasonMalformedEVM {
		t.Fatalf("truncated HMAC reason = %s, want %s", result.Reason, ReasonMalformedEVM)
	}

	state.EVM = nil
	if result := NewEvaluator(key).Evaluate(state); result.Allowed() {
		t.Fatal("missing EVM was accepted")
	} else if result.Reason != ReasonMissingEVM {
		t.Fatalf("missing EVM reason = %s, want %s", result.Reason, ReasonMissingEVM)
	}
}

func TestLegacyHMACSHA1(t *testing.T) {
	key := testKey()
	state := baseState()
	record, err := SealHMACSHA1(state, key)
	if err != nil {
		t.Fatal(err)
	}
	state.EVM = record
	if result := NewEvaluator(key).Evaluate(state); !result.Allowed() {
		t.Fatalf("legacy EVM HMAC-SHA-1 denied: %s", result)
	}
}

func TestHMACBindsEveryProtectedField(t *testing.T) {
	key := testKey()
	base := baseState()
	record := mustSealHMAC(t, base, key)

	tests := map[string]func(*FileState){
		"ima":        func(s *FileState) { s.IMA = []byte("changed-ima") },
		"selinux":    func(s *FileState) { s.SELinux = []byte("changed-selinux") },
		"inode":      func(s *FileState) { s.Inode++ },
		"generation": func(s *FileState) { s.Generation++ },
		"uid":        func(s *FileState) { s.UID++ },
		"gid":        func(s *FileState) { s.GID++ },
		"mode":       func(s *FileState) { s.Mode ^= 0o002 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			changed.EVM = record
			if result := NewEvaluator(key).Evaluate(changed); result.Allowed() {
				t.Fatalf("%s change was accepted", name)
			}
		})
	}
}

func TestHMACInputOrderAndPortableExclusion(t *testing.T) {
	state := baseState()
	wantLocal := appendBytes(nil, state.IMA)
	wantLocal = appendBytes(wantLocal, state.SELinux)
	wantLocal = appendUint64(wantLocal, state.Inode)
	wantLocal = appendUint64(wantLocal, state.Generation)
	wantLocal = appendUint32(wantLocal, state.UID)
	wantLocal = appendUint32(wantLocal, state.GID)
	wantLocal = appendUint32(wantLocal, state.Mode)
	if got := HMACInput(state); !bytes.Equal(got, wantLocal) {
		t.Fatalf("HMAC input = %x, want %x", got, wantLocal)
	}

	wantPortable := appendBytes(nil, state.IMA)
	wantPortable = appendBytes(wantPortable, state.SELinux)
	wantPortable = appendUint32(wantPortable, state.UID)
	wantPortable = appendUint32(wantPortable, state.GID)
	wantPortable = appendUint32(wantPortable, state.Mode)
	if got := PortableSignatureInput(state); !bytes.Equal(got, wantPortable) {
		t.Fatalf("portable input = %x, want %x", got, wantPortable)
	}
}

func baseState() FileState {
	return FileState{
		Path:       "/tmp/base",
		IMA:        []byte("ima-digest-v1"),
		SELinux:    []byte("system_u:object_r:usr_t:s0"),
		Inode:      9001,
		Generation: 77,
		UID:        1000,
		GID:        1000,
		Mode:       0o100644,
	}
}

func testKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

func mustSealHMAC(t *testing.T, state FileState, key []byte) []byte {
	t.Helper()
	record, err := SealHMAC(state, key)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func mustSignPortable(t *testing.T, state FileState, signer crypto.Signer) []byte {
	t.Helper()
	record, err := SignPortable(state, signer)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func createCopy(t *testing.T) (string, string, uint64, uint64) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "original")
	destination := filepath.Join(dir, "copied")
	content := []byte("copy me")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("copy unexpectedly shares an inode")
	}
	return source, destination, inodeNumber(t, sourceInfo), inodeNumber(t, destinationInfo)
}

func inodeNumber(t *testing.T, info os.FileInfo) uint64 {
	t.Helper()
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("Ino")
	if !field.IsValid() {
		t.Fatal("stat structure has no inode field")
	}
	return field.Uint()
}
