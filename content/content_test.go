package content

import (
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(t *testing.T, algo Algorithm, data []byte) Digest {
	t.Helper()
	var sum []byte
	switch algo {
	case SHA256:
		h := sha256.Sum256(data)
		sum = h[:]
	case SHA512:
		h := sha512.Sum512(data)
		sum = h[:]
	}
	d, err := ParseDigest(fmt.Sprintf("%s:%x", algo, sum))
	if err != nil {
		t.Fatalf("ParseDigest: %v", err)
	}
	return d
}

func mustWrite(t *testing.T, s *Session, p []byte) {
	t.Helper()
	if _, err := s.Write(p); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func sessionExists(t *testing.T, st *Store, ref string) bool {
	t.Helper()
	_, err := os.Stat(st.sessionDir(ref))
	return err == nil
}

func blobContent(t *testing.T, st *Store, d Digest) []byte {
	t.Helper()
	f, err := st.OpenBlob(d)
	if err != nil {
		t.Fatalf("open blob: %v", err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	return b
}

// Tampering with the temp file during the interruption must be caught,
// because Resume rebuilds the digest state from the bytes on disk.
func TestResumeDetectsTampering(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	part1 := []byte("hello ")
	part2 := []byte("world")
	full := append(append([]byte{}, part1...), part2...)
	good := digestOf(t, SHA256, full)

	sess, err := st.Begin("layer-1", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sess, part1)

	// Attacker rewrites the temp file while the session is interrupted.
	dataPath := filepath.Join(root, "sessions", "layer-1", "data")
	f, err := os.OpenFile(dataPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("j"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()

	resumed, err := st.Resume("layer-1", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Offset() != int64(len(part1)) {
		t.Fatalf("offset = %d, want %d", resumed.Offset(), len(part1))
	}
	mustWrite(t, resumed, part2)

	err = resumed.Commit(good, int64(len(full)))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("commit err = %v, want ErrDigestMismatch", err)
	}
	if sessionExists(t, st, "layer-1") {
		t.Fatal("session dir still exists after failed commit")
	}
	if st.HasBlob(good) {
		t.Fatal("blob created for digest whose content never matched")
	}
}

// Committing content that does not match the declared digest must fail,
// delete the session, and not squat on the digest name.
func TestCommitWrongDigest(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("real content")
	claimed := digestOf(t, SHA256, []byte("attacker's content"))

	sess, err := st.Begin("evil", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sess, content)

	err = sess.Commit(claimed, int64(len(content)))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("commit err = %v, want ErrDigestMismatch", err)
	}
	if sessionExists(t, st, "evil") {
		t.Fatal("session dir still exists after failed commit")
	}
	if st.HasBlob(claimed) {
		t.Fatal("blob squatted on a digest it does not match")
	}
}

// A wrong declared size must also abort the session without a blob.
func TestCommitWrongSize(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("some bytes")
	d := digestOf(t, SHA256, content)

	sess, err := st.Begin("size", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sess, content)

	err = sess.Commit(d, int64(len(content)+1))
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("commit err = %v, want ErrSizeMismatch", err)
	}
	if sessionExists(t, st, "size") {
		t.Fatal("session dir still exists after failed commit")
	}
	if st.HasBlob(d) {
		t.Fatal("blob created despite size mismatch")
	}
}

// Re-committing the same digest only verifies content consistency and
// leaves the original read-only blob untouched.
func TestDuplicateCommitSameDigest(t *testing.T) {
	root := t.TempDir()
	st, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("deduplicated layer")
	d := digestOf(t, SHA256, content)

	sess, err := st.Begin("first", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sess, content)
	if err := sess.Commit(d, int64(len(content))); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	info, err := os.Stat(st.blobPath(d))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("blob mode = %o, want 0444 (read-only)", info.Mode().Perm())
	}

	sess2, err := st.Begin("second", SHA256)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, sess2, content)
	if err := sess2.Commit(d, int64(len(content))); err != nil {
		t.Fatalf("duplicate commit: %v", err)
	}
	if sessionExists(t, st, "second") {
		t.Fatal("duplicate session not cleaned up")
	}
	if got := blobContent(t, st, d); string(got) != string(content) {
		t.Fatalf("blob content = %q, want %q", got, content)
	}
}

// A clean interruption and resume must produce exactly the committed blob.
func TestLegitResume(t *testing.T) {
	for _, algo := range []Algorithm{SHA256, SHA512} {
		t.Run(string(algo), func(t *testing.T) {
			root := t.TempDir()
			st, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}

			content := []byte(strings.Repeat("resumable-", 100))
			cut := int64(137)
			d := digestOf(t, algo, content)

			sess, err := st.Begin("pull", algo)
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, sess, content[:cut])

			off, err := st.SessionOffset("pull")
			if err != nil {
				t.Fatal(err)
			}
			if off != cut {
				t.Fatalf("persisted offset = %d, want %d", off, cut)
			}

			resumed, err := st.Resume("pull", algo)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Offset() != cut {
				t.Fatalf("resumed offset = %d, want %d", resumed.Offset(), cut)
			}
			mustWrite(t, resumed, content[cut:])

			if err := resumed.Commit(d, int64(len(content))); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if !st.HasBlob(d) {
				t.Fatal("blob missing after commit")
			}
			if got := blobContent(t, st, d); string(got) != string(content) {
				t.Fatal("blob content mismatch")
			}
			if sessionExists(t, st, "pull") {
				t.Fatal("session dir not cleaned up after commit")
			}
		})
	}
}

func TestParseDigest(t *testing.T) {
	ok := []string{
		"sha256:" + strings.Repeat("a", 64),
		"sha512:" + strings.Repeat("0", 128),
	}
	for _, s := range ok {
		if _, err := ParseDigest(s); err != nil {
			t.Errorf("ParseDigest(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{
		"md5:" + strings.Repeat("a", 32),
		"sha1:" + strings.Repeat("a", 40),
		"sha256:" + strings.Repeat("a", 63),
		"sha256:" + strings.Repeat("a", 65),
		"sha512:" + strings.Repeat("a", 64),
		"sha256:" + strings.Repeat("zz", 32),
		"sha256",
		":" + strings.Repeat("a", 64),
		"",
	}
	for _, s := range bad {
		if _, err := ParseDigest(s); err == nil {
			t.Errorf("ParseDigest(%q) = nil, want error", s)
		}
	}
}
