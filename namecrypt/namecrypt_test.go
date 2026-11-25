package namecrypt

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// collisionHasher truncates the real HMAC to a single output byte, so distinct
// canonical names frequently share a lookup bucket. It stands in for a
// collision-prone HMAC to prove digest-colliding entries coexist.
type collisionHasher struct{}

func (collisionHasher) LookupKey(nameKey []byte, canonical string) [LookupKeySize]byte {
	mac := hmac.New(sha256.New, nameKey)
	mac.Write([]byte("lookup"))
	mac.Write([]byte(canonical))
	sum := mac.Sum(nil)
	var key [LookupKeySize]byte
	for i := range key {
		key[i] = sum[0]
	}
	return key
}

func itoa(i int) string { return strconv.Itoa(i) }

func testPolicy(id DirectoryID, mode CaseMode) Policy {
	var nonce [NonceSize]byte
	for i := range nonce {
		nonce[i] = byte(i + 1)
	}
	return Policy{
		DirectoryID:       id,
		KeyGeneration:     1,
		DirectoryNonce:    nonce,
		CaseMode:          mode,
		MaxRawNameBytes:   255,
		MaxCanonicalBytes: 255,
	}
}

func TestValidateNameAndPolicy(t *testing.T) {
	bad := []string{
		"", ".", "..", "a/b", "a\x00b",
		string([]byte{0xff, 0xfe}),
	}
	for _, name := range bad {
		if err := ValidateName(name); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("ValidateName(%q) = %v, want ErrInvalidName", name, err)
		}
	}
	for _, name := range []string{"a", "..x", ".x", "dir name", "café", "世界"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q) unexpected error: %v", name, err)
		}
	}

	p := testPolicy("d", CaseSensitive)
	if err := p.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	zeroNonce := p
	zeroNonce.DirectoryNonce = [NonceSize]byte{}
	if err := zeroNonce.Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("zero nonce policy = %v, want ErrInvalidPolicy", err)
	}
}

func TestCanonicalize(t *testing.T) {
	if got := Canonicalize("AbC-123", CaseSensitive); got != "AbC-123" {
		t.Fatalf("sensitive canonical = %q", got)
	}
	if got := Canonicalize("AbC-123", CaseFoldASCII); got != "abc-123" {
		t.Fatalf("ascii fold = %q", got)
	}
	raw := "CAFÉ_世界"
	got := Canonicalize(raw, CaseFoldASCII)
	want := "caf" + string([]byte{0xc3, 0x89}) + "_世界"
	if got != want {
		t.Fatalf("non-ascii fold = %q, want %q", got, want)
	}
}

func TestLengthLimits(t *testing.T) {
	p := testPolicy("d", CaseSensitive)
	p.MaxRawNameBytes = 3
	dir, err := NewDirectory(p, []byte("master"), NewMemoryStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.Create("abcd", InodeRef{ID: "i1"}); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("raw length = %v, want ErrNameTooLong", err)
	}

	fold := testPolicy("d2", CaseFoldASCII)
	fold.MaxCanonicalBytes = 2
	foldDir, err := NewDirectory(fold, []byte("master"), NewMemoryStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := foldDir.Create("ABC", InodeRef{ID: "i1"}); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("canonical length = %v, want ErrNameTooLong", err)
	}
}

func TestSensitiveCreateLookup(t *testing.T) {
	dir, _ := NewDirectory(testPolicy("d", CaseSensitive), []byte("master"), NewMemoryStore(), nil)
	inode := InodeRef{ID: "ino", Generation: 7}
	if err := dir.Create("File.txt", inode); err != nil {
		t.Fatal(err)
	}
	gotInode, raw, err := dir.Lookup("File.txt")
	if err != nil {
		t.Fatal(err)
	}
	if gotInode != inode || raw != "File.txt" {
		t.Fatalf("lookup = %+v %q", gotInode, raw)
	}
	if _, _, err := dir.Lookup("file.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("case-sensitive lower lookup = %v, want ErrNotFound", err)
	}
	if _, err := dir.LookupInode("File.txt", InodeRef{ID: "ino", Generation: 7}); err != nil {
		t.Fatalf("LookupInode correct identity failed: %v", err)
	}
	if _, err := dir.LookupInode("File.txt", InodeRef{ID: "ino", Generation: 8}); !errors.Is(err, ErrInodeMismatch) {
		t.Fatalf("LookupInode wrong generation = %v", err)
	}
	if _, err := dir.LookupInode("File.txt", InodeRef{ID: "other", Generation: 7}); !errors.Is(err, ErrInodeMismatch) {
		t.Fatalf("LookupInode wrong id = %v", err)
	}
}

func TestFoldASCIICanonicalConflict(t *testing.T) {
	dir, _ := NewDirectory(testPolicy("d", CaseFoldASCII), []byte("master"), NewMemoryStore(), nil)
	if err := dir.Create("Abc", InodeRef{ID: "i1"}); err != nil {
		t.Fatal(err)
	}
	if err := dir.Create("abc", InodeRef{ID: "i2"}); !errors.Is(err, ErrCanonicalConflict) {
		t.Fatalf("fold conflict = %v, want ErrCanonicalConflict", err)
	}
	if _, _, err := dir.Lookup("ABC"); err != nil {
		t.Fatalf("folded lookup failed: %v", err)
	}
	inode, raw, err := dir.Lookup("abc")
	if err != nil {
		t.Fatal(err)
	}
	if inode.ID != "i1" || raw != "Abc" {
		t.Fatalf("original entry overwritten: %+v %q", inode, raw)
	}
	if err := dir.Create("Abc", InodeRef{ID: "i1"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate = %v, want ErrExists", err)
	}
}

func TestNonASCIINamesRoundTrip(t *testing.T) {
	dir, _ := NewDirectory(testPolicy("d", CaseFoldASCII), []byte("master"), NewMemoryStore(), nil)
	names := []string{"café", "世界.txt", "Ωmega", "ÉCOLE", "ñoño_123"}
	for i, name := range names {
		ref := InodeRef{ID: InodeID(string(rune('a' + i))), Generation: 1}
		if err := dir.Create(name, ref); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
	}
	for i, name := range names {
		inode, raw, err := dir.Lookup(name)
		if err != nil {
			t.Fatalf("lookup %q: %v", name, err)
		}
		want := InodeID(string(rune('a' + i)))
		if raw != name || inode.ID != want {
			t.Fatalf("round trip %q -> %q %+v", name, raw, inode)
		}
	}
	if err := dir.Create("ecole", InodeRef{ID: "sep"}); err != nil {
		t.Fatalf("distinct non-ascii name rejected: %v", err)
	}
}

func TestCollisionHasherCoexistence(t *testing.T) {
	dir, _ := NewDirectory(testPolicy("d", CaseSensitive), []byte("master"), NewMemoryStore(), collisionHasher{})
	const n = 40
	keys := map[string]InodeRef{}
	for i := 0; i < n; i++ {
		name := "entry-" + strings.Repeat("z", i%5) + "-" + string(rune('A'+i%26)) + "-" + itoa(i)
		inode := InodeRef{ID: InodeID("ino-" + itoa(i)), Generation: uint64(i + 1)}
		if err := dir.Create(name, inode); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		keys[name] = inode
	}
	if len(dir.store.(*MemoryStore).buckets) >= n {
		t.Fatalf("collision hasher did not collapse buckets")
	}
	for name, inode := range keys {
		gotInode, raw, err := dir.Lookup(name)
		if err != nil {
			t.Fatalf("lookup in colliding bucket %q: %v", name, err)
		}
		if gotInode != inode || raw != name {
			t.Fatalf("identity mismatch for %q: %+v %q", name, gotInode, raw)
		}
	}
}

func TestHardLinksSameInode(t *testing.T) {
	dir, _ := NewDirectory(testPolicy("d", CaseSensitive), []byte("master"), NewMemoryStore(), nil)
	target := InodeRef{ID: "inode-9", Generation: 3}
	links := []string{"a", "b", "link3"}
	for _, name := range links {
		if err := dir.Create(name, target); err != nil {
			t.Fatalf("hard link create %q: %v", name, err)
		}
	}
	for _, name := range links {
		if _, err := dir.LookupInode(name, target); err != nil {
			t.Fatalf("hard link %q identity: %v", name, err)
		}
	}
	if _, err := dir.LookupInode("a", InodeRef{ID: "inode-9", Generation: 4}); !errors.Is(err, ErrInodeMismatch) {
		t.Fatalf("stale inode generation = %v", err)
	}
}

func TestRenameSameDirectory(t *testing.T) {
	store := NewMemoryStore()
	dir, _ := NewDirectory(testPolicy("d", CaseSensitive), []byte("master"), store, nil)
	inode := InodeRef{ID: "ino", Generation: 1}
	if err := dir.Create("old", inode); err != nil {
		t.Fatal(err)
	}
	if err := dir.Rename("old", "new", dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := dir.Lookup("old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name still present: %v", err)
	}
	if _, err := dir.LookupInode("new", inode); err != nil {
		t.Fatalf("new name lookup: %v", err)
	}
	if err := dir.Create("other", InodeRef{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := dir.Rename("new", "other", dir); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite rename = %v, want ErrExists", err)
	}
}

func TestRenameSwapAndCrossPolicy(t *testing.T) {
	store := NewMemoryStore()
	src, _ := NewDirectory(testPolicy("dir-a", CaseSensitive), []byte("master"), store, nil)
	dst, _ := NewDirectory(testPolicy("dir-b", CaseFoldASCII), []byte("master"), store, collisionHasher{})

	inoA := InodeRef{ID: "a", Generation: 1}
	inoB := InodeRef{ID: "b", Generation: 1}
	if err := src.Create("Alpha", inoA); err != nil {
		t.Fatal(err)
	}
	if err := dst.Create("beta", inoB); err != nil {
		t.Fatal(err)
	}

	if err := src.Rename("Alpha", "alpha-tmp", dst); err != nil {
		t.Fatalf("rename a->b: %v", err)
	}
	if err := dst.Rename("beta", "Beta", src); err != nil {
		t.Fatalf("rename b->a: %v", err)
	}
	if err := dst.Rename("alpha-tmp", "alpha", dst); err != nil {
		t.Fatalf("finalize a: %v", err)
	}

	if _, err := dst.LookupInode("ALPHA", inoA); err != nil {
		t.Fatalf("folded lookup of migrated a: %v", err)
	}
	if _, err := src.LookupInode("Beta", inoB); err != nil {
		t.Fatalf("lookup of migrated b: %v", err)
	}
	if _, _, err := src.Lookup("Alpha"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source entry not removed")
	}

	if err := src.Create("gamma", InodeRef{ID: "g"}); err != nil {
		t.Fatal(err)
	}
	if err := src.Rename("gamma", "ALPHA", dst); !errors.Is(err, ErrExists) {
		t.Fatalf("conflicting cross rename = %v", err)
	}
	if _, err := src.LookupInode("gamma", InodeRef{ID: "g"}); err != nil {
		t.Fatalf("source removed on failed rename: %v", err)
	}
}

func TestRenameAcrossStoresKeepsSourceOnFailure(t *testing.T) {
	srcStore, dstStore := NewMemoryStore(), NewMemoryStore()
	src, _ := NewDirectory(testPolicy("dir-a", CaseSensitive), []byte("master"), srcStore, nil)
	dst, _ := NewDirectory(testPolicy("dir-b", CaseSensitive), []byte("master"), dstStore, nil)

	ref := InodeRef{ID: "ino", Generation: 5}
	if err := src.Create("file", ref); err != nil {
		t.Fatal(err)
	}
	// Destination conflict: the destination commit never happens and the source
	// stays fully readable.
	if err := dst.Create("taken", InodeRef{ID: "other"}); err != nil {
		t.Fatal(err)
	}
	if err := src.Rename("file", "taken", dst); !errors.Is(err, ErrExists) {
		t.Fatalf("cross-store conflict = %v, want ErrExists", err)
	}
	if _, err := src.LookupInode("file", ref); err != nil {
		t.Fatalf("source lost on failed cross-store rename: %v", err)
	}

	// Successful cross-store rename reseals under the destination policy/store.
	if err := src.Rename("file", "moved", dst); err != nil {
		t.Fatalf("cross-store rename: %v", err)
	}
	if _, _, err := src.Lookup("file"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("source still present after cross-store move")
	}
	if _, err := dst.LookupInode("moved", ref); err != nil {
		t.Fatalf("destination missing moved entry: %v", err)
	}
	if len(srcStore.buckets) != 0 {
		t.Fatalf("source store not emptied: %d buckets", len(srcStore.buckets))
	}
}

func TestRotationInterruptedAndResume(t *testing.T) {
	store := NewMemoryStore()
	dir, _ := NewDirectory(testPolicy("d", CaseFoldASCII), []byte("master"), store, collisionHasher{})
	names := []string{"One", "Two", "Three", "Four", "Five"}
	inodes := map[string]InodeRef{}
	for i, name := range names {
		ref := InodeRef{ID: InodeID("ino-" + itoa(i)), Generation: 2}
		inodes[name] = ref
		if err := dir.Create(name, ref); err != nil {
			t.Fatal(err)
		}
	}

	migrated := 0
	dir.RotationHook = func(canonical string, from, to uint64) error {
		migrated++
		if migrated == 3 {
			return errors.New("simulated crash")
		}
		return nil
	}
	if err := dir.RotateKey(2); err == nil {
		t.Fatal("expected interrupted rotation error")
	}
	if !dir.IsRotating() {
		t.Fatal("directory should report rotation in progress")
	}

	// Every name still resolves while the directory spans both generations.
	for name, ref := range inodes {
		if _, err := dir.LookupInode(name, ref); err != nil {
			t.Fatalf("lookup during rotation %q: %v", name, err)
		}
	}
	// Writes are blocked until rotation completes.
	if err := dir.Create("Six", InodeRef{ID: "ino-5"}); !errors.Is(err, ErrRotationInProgress) {
		t.Fatalf("create during rotation = %v", err)
	}

	// Old-generation ciphertext must not be usable through the new generation's
	// lookup key: the two keys differ and AEAD binding includes the generation.
	var oldGen, newGen int
	for _, records := range store.buckets {
		for _, rec := range records {
			if rec.KeyGeneration == 1 {
				oldGen++
			}
			if rec.KeyGeneration == 2 {
				newGen++
			}
			newKey := dir.lookupKey(rec.Canonical, 2)
			oldKey := dir.lookupKey(rec.Canonical, 1)
			if rec.KeyGeneration == 1 && newKey == oldKey {
				t.Fatal("new generation lookup key reused old generation key")
			}
		}
	}
	if oldGen == 0 || newGen == 0 {
		t.Fatalf("expected mixed generations after interruption: old=%d new=%d", oldGen, newGen)
	}

	// A migrated entry is freshly sealed: the old ciphertext must not be
	// reused as new-generation material.
	for _, records := range store.buckets {
		for _, rec := range records {
			if rec.KeyGeneration != 2 {
				continue
			}
			oldBucket := dir.lookupKey(rec.Canonical, 1)
			for _, old := range store.buckets[oldBucket] {
				if old.Canonical == rec.Canonical && bytes.Equal(old.Ciphertext, rec.Ciphertext) {
					t.Fatalf("new generation reused old ciphertext for %q", rec.Canonical)
				}
				if old.Canonical == rec.Canonical && bytes.Equal(old.EntryNonce, rec.EntryNonce) {
					t.Fatalf("new generation reused old entry nonce for %q", rec.Canonical)
				}
			}
		}
	}

	dir.RotationHook = nil
	if err := dir.ResumeRotation(); err != nil {
		t.Fatalf("resume rotation: %v", err)
	}
	if dir.IsRotating() || dir.Policy().KeyGeneration != 2 {
		t.Fatalf("rotation not finished: %+v rotating=%v", dir.Policy(), dir.IsRotating())
	}
	for name, ref := range inodes {
		if _, err := dir.LookupInode(name, ref); err != nil {
			t.Fatalf("lookup after rotation %q: %v", name, err)
		}
	}
	for _, records := range store.buckets {
		for _, rec := range records {
			if rec.KeyGeneration != 2 {
				t.Fatalf("stale generation %d after resume", rec.KeyGeneration)
			}
		}
	}

	if err := dir.RotateKey(2); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("non-increasing generation = %v", err)
	}

	idle, _ := NewDirectory(testPolicy("idle", CaseSensitive), []byte("m"), NewMemoryStore(), nil)
	if err := idle.ResumeRotation(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("resume without rotation = %v", err)
	}
}

func TestPolicyDigestAndTamperProtection(t *testing.T) {
	store := NewMemoryStore()
	dir, _ := NewDirectory(testPolicy("d", CaseSensitive), []byte("master"), store, nil)
	ref := InodeRef{ID: "ino", Generation: 1}
	if err := dir.Create("secret.txt", ref); err != nil {
		t.Fatal(err)
	}

	// Tamper with ciphertext: AEAD authentication must reject it.
	var target *Record
	for _, records := range store.buckets {
		for _, rec := range records {
			if rec.Canonical == "secret.txt" {
				target = rec
			}
		}
	}
	if target == nil {
		t.Fatal("record not found")
	}
	tamperedCipher := append([]byte(nil), target.Ciphertext...)
	tamperedCipher[0] ^= 0xFF
	target.Ciphertext = tamperedCipher
	if _, _, err := dir.Lookup("secret.txt"); !errors.Is(err, ErrBadRecord) {
		t.Fatalf("tampered ciphertext = %v, want ErrBadRecord", err)
	}

	// The canonical name hash is AEAD-bound as well: tampering it makes the
	// decryption of the now-mislabeled record fail.
	target.Ciphertext = append([]byte(nil), tamperedCipher...) // restore
	target.Ciphertext[0] ^= 0xFF
	target.Canonical = "tampered.txt"
	bucket := dir.lookupKey("tampered.txt", 1)
	store.mu.Lock()
	store.buckets[bucket] = append(store.buckets[bucket], target)
	store.mu.Unlock()
	if _, _, err := dir.Lookup("tampered.txt"); !errors.Is(err, ErrBadRecord) {
		t.Fatalf("tampered canonical binding = %v, want ErrBadRecord", err)
	}

	// Policy digest binding: a directory with a different nonce derives another
	// name key and cannot decrypt the record.
	otherPolicy := testPolicy("d", CaseSensitive)
	otherPolicy.DirectoryNonce[0] ^= 0xFF
	other, _ := NewDirectory(otherPolicy, []byte("master"), NewMemoryStore(), nil)
	if _, err := other.open(target); !errors.Is(err, ErrBadRecord) {
		t.Fatalf("cross-policy decrypt = %v, want ErrBadRecord", err)
	}
}
