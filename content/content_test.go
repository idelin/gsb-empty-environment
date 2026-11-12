package content

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustDigest(t *testing.T, algo string, p []byte) Digest {
	t.Helper()
	d, err := FromBytes(algo, p)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}
	return d
}

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func blobExists(t *testing.T, s *Store, d Digest) bool {
	t.Helper()
	_, err := os.Stat(s.blobPath(d))
	return err == nil
}

func sessionExists(s *Store, ref string) bool {
	dataPath, metaPath := s.ingestPaths(ref)
	_, dataErr := os.Stat(dataPath)
	_, metaErr := os.Stat(metaPath)
	return dataErr == nil || metaErr == nil
}

// 恢复前篡改：中断期间临时文件被改写，恢复时从头重算摘要，
// 提交必须失败、会话被删除且不产生 blob。
func TestResumeTamperedIngest(t *testing.T) {
	s := newStore(t)
	content := bytes.Repeat([]byte("mirror-layer-"), 100)
	expected := mustDigest(t, AlgorithmSHA256, content)

	w, err := s.Writer("layer-1", int64(len(content)), expected)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	half := len(content) / 2
	if _, err := w.Write(content[:half]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 攻击者在中断期间改写临时文件。
	dataPath, _ := s.ingestPaths("layer-1")
	f, err := os.OpenFile(dataPath, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open ingest: %v", err)
	}
	if _, err := f.WriteAt([]byte("PWNED"), 3); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	f.Close()

	// 恢复会话：摘要状态从磁盘数据重算，而不是沿用保存的状态。
	w2, err := s.Writer("layer-1", int64(len(content)), expected)
	if err != nil {
		t.Fatalf("resume Writer: %v", err)
	}
	if got := w2.Status().Offset; got != int64(half) {
		t.Fatalf("resumed offset = %d, want %d", got, half)
	}
	if _, err := w2.Write(content[half:]); err != nil {
		t.Fatalf("Write after resume: %v", err)
	}
	err = w2.Commit(int64(len(content)), expected)
	if err == nil {
		t.Fatal("Commit of tampered session succeeded, want digest mismatch error")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Commit error = %v, want digest mismatch", err)
	}
	if blobExists(t, s, expected) {
		t.Fatal("blob created for tampered content")
	}
	if sessionExists(s, "layer-1") {
		t.Fatal("session not removed after failed commit")
	}
}

// 错摘要提交：提交与声明摘要不同的内容，不能占住该摘要。
func TestCommitWrongDigest(t *testing.T) {
	s := newStore(t)
	content := []byte("attacker controlled content")
	claimed := mustDigest(t, AlgorithmSHA256, []byte("innocent image layer"))

	w, err := s.Writer("evil", int64(len(content)), claimed)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Commit(int64(len(content)), claimed); err == nil {
		t.Fatal("Commit with mismatched content succeeded")
	}
	if blobExists(t, s, claimed) {
		t.Fatal("attacker occupied the claimed digest")
	}
	if sessionExists(s, "evil") {
		t.Fatal("session not removed after failed commit")
	}

	// 大小不符同样必须失败。
	content2 := []byte("exactly the content matching the digest, trust me")
	expected2 := mustDigest(t, AlgorithmSHA256, content2)
	w2, err := s.Writer("bad-size", int64(len(content2)), expected2)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w2.Write(content2); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w2.Commit(int64(len(content2))+1, expected2); err == nil {
		t.Fatal("Commit with wrong size succeeded")
	}
	if blobExists(t, s, expected2) {
		t.Fatal("blob created after size mismatch")
	}
	if sessionExists(s, "bad-size") {
		t.Fatal("session not removed after size mismatch")
	}
}

// 同摘要重复提交：只校验内容一致，blob 保持只读且不被覆盖。
func TestDuplicateCommitSameDigest(t *testing.T) {
	s := newStore(t)
	content := []byte("deduplicated blob content")
	expected := mustDigest(t, AlgorithmSHA512, content)

	commit := func(ref string) {
		t.Helper()
		w, err := s.Writer(ref, int64(len(content)), expected)
		if err != nil {
			t.Fatalf("Writer: %v", err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := w.Commit(int64(len(content)), expected); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}

	commit("ref-a")
	info, err := os.Stat(s.blobPath(expected))
	if err != nil {
		t.Fatalf("stat blob: %v", err)
	}
	if info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("blob is writable: mode %v", info.Mode())
	}
	modTime := info.ModTime()

	commit("ref-b")
	info2, err := os.Stat(s.blobPath(expected))
	if err != nil {
		t.Fatalf("stat blob after duplicate commit: %v", err)
	}
	if !info2.ModTime().Equal(modTime) {
		t.Fatal("duplicate commit rewrote the existing blob")
	}
	if sessionExists(s, "ref-b") {
		t.Fatal("duplicate commit left session files behind")
	}
	got, err := s.Get(expected)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("blob content changed after duplicate commit")
	}
}

// 合法续传：中断后恢复会话，偏移正确，提交成功。
func TestResumeSuccess(t *testing.T) {
	for _, algo := range []string{AlgorithmSHA256, AlgorithmSHA512} {
		t.Run(algo, func(t *testing.T) {
			s := newStore(t)
			content := bytes.Repeat([]byte("resumable-blob-"), 500)
			expected := mustDigest(t, algo, content)

			w, err := s.Writer("good-ref", int64(len(content)), expected)
			if err != nil {
				t.Fatalf("Writer: %v", err)
			}
			cut := len(content) / 3
			if _, err := w.Write(content[:cut]); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			w2, err := s.Writer("good-ref", int64(len(content)), expected)
			if err != nil {
				t.Fatalf("resume Writer: %v", err)
			}
			st := w2.Status()
			if st.Offset != int64(cut) {
				t.Fatalf("resumed offset = %d, want %d", st.Offset, cut)
			}
			if st.Expected != expected {
				t.Fatalf("resumed expected = %s, want %s", st.Expected, expected)
			}
			if _, err := w2.Write(content[cut:]); err != nil {
				t.Fatalf("Write after resume: %v", err)
			}
			if err := w2.Commit(int64(len(content)), expected); err != nil {
				t.Fatalf("Commit: %v", err)
			}
			if !blobExists(t, s, expected) {
				t.Fatal("blob missing after commit")
			}
			got, err := s.Get(expected)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !bytes.Equal(got, content) {
				t.Fatal("blob content mismatch")
			}
			if sessionExists(s, "good-ref") {
				t.Fatal("session files left behind after commit")
			}
		})
	}
}

// 摘要前缀与长度校验。
func TestDigestValidation(t *testing.T) {
	valid256 := fmt.Sprintf("sha256:%064x", 1)
	valid512 := fmt.Sprintf("sha512:%0128x", 1)
	cases := []struct {
		digest  string
		wantErr bool
	}{
		{valid256, false},
		{valid512, false},
		{"sha1:" + strings.Repeat("a", 40), true},   // 不支持的算法
		{"md5:" + strings.Repeat("a", 32), true},    // 不支持的算法
		{"sha256:" + strings.Repeat("a", 63), true}, // 长度不足
		{"sha256:" + strings.Repeat("a", 65), true}, // 长度超长
		{"sha512:" + strings.Repeat("a", 64), true}, // sha512 用了 sha256 长度
		{"sha256:" + strings.Repeat("zz", 32), true},
		{"sha256:" + strings.Repeat("A", 64), true}, // 必须小写
		{strings.Repeat("a", 64), true},             // 缺少前缀
		{"sha256:", true},
		{"", true},
	}
	for _, c := range cases {
		err := Digest(c.digest).Validate()
		if (err != nil) != c.wantErr {
			t.Errorf("Validate(%q) err = %v, wantErr %v", c.digest, err, c.wantErr)
		}
	}

	// Store 层同样拒绝非法摘要。
	s := newStore(t)
	if _, err := s.Writer("r", 1, Digest("sha1:"+strings.Repeat("a", 40))); err == nil {
		t.Fatal("Writer accepted sha1 digest")
	}
	if _, err := s.Writer("r", 1, Digest("sha256:"+strings.Repeat("a", 10))); err == nil {
		t.Fatal("Writer accepted short sha256 digest")
	}
	if _, err := s.Reader(Digest("md5:" + strings.Repeat("a", 32))); err == nil {
		t.Fatal("Reader accepted md5 digest")
	}
}

// 提交后的 blob 必须只读。
func TestBlobReadOnly(t *testing.T) {
	s := newStore(t)
	content := []byte("immutable")
	expected := mustDigest(t, AlgorithmSHA256, content)

	w, err := s.Writer("ro", int64(len(content)), expected)
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Commit(int64(len(content)), expected); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	path := filepath.Join(s.root, "blobs", AlgorithmSHA256, expected.Encoded())
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat blob: %v", err)
	}
	if info.Mode().Perm() != 0o444 {
		t.Fatalf("blob mode = %v, want 0444", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("evil"), 0o644); err == nil {
		t.Fatal("committed blob is writable")
	}
}
