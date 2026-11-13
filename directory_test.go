package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func newTestHost(t *testing.T, opts ...HostOption) *Host {
	t.Helper()
	h, err := NewHost([]byte("test-master-handle-0123456789ab"), opts...)
	if err != nil {
		t.Fatalf("NewHost: %v", err)
	}
	return h
}

func newTestDir(t *testing.T, h *Host, mode CaseMode) *Directory {
	t.Helper()
	p, err := NewPolicy(mode, 255, 255)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	d, err := h.NewDirectory(p)
	if err != nil {
		t.Fatalf("NewDirectory: %v", err)
	}
	return d
}

func mustLookup(t *testing.T, d *Directory, name string, wantInode, wantGen uint64) string {
	t.Helper()
	raw, inode, gen, err := d.Lookup(name)
	if err != nil {
		t.Fatalf("Lookup(%q): %v", name, err)
	}
	if inode != wantInode || gen != wantGen {
		t.Fatalf("Lookup(%q) inode = (%d,%d), want (%d,%d)", name, inode, gen, wantInode, wantGen)
	}
	return raw
}

func TestCreateAndLookup(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	if err := d.Create("hello.txt", 100, 1); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if raw := mustLookup(t, d, "hello.txt", 100, 1); raw != "hello.txt" {
		t.Fatalf("resolved = %q", raw)
	}
	if _, _, _, err := d.Lookup("missing"); err != ErrNotFound {
		t.Fatalf("Lookup(missing) = %v, want ErrNotFound", err)
	}
	if err := d.Create("hello.txt", 100, 1); err != ErrExist {
		t.Fatalf("duplicate Create = %v, want ErrExist", err)
	}
}

func TestSensitiveCaseCoexist(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	if err := d.Create("Foo", 1, 1); err != nil {
		t.Fatalf("Create Foo: %v", err)
	}
	if err := d.Create("foo", 2, 1); err != nil {
		t.Fatalf("Create foo: %v", err)
	}
	mustLookup(t, d, "Foo", 1, 1)
	mustLookup(t, d, "foo", 2, 1)
}

func TestFoldASCIIConflict(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseFoldASCII)
	if err := d.Create("Foo.txt", 7, 3); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 规范名相同、原始名不同：拒绝。
	if err := d.Create("foo.TXT", 8, 1); err != ErrCanonicalConflict {
		t.Fatalf("Create(foo.TXT) = %v, want ErrCanonicalConflict", err)
	}
	// 原始名完全相同：已存在。
	if err := d.Create("Foo.txt", 9, 1); err != ErrExist {
		t.Fatalf("Create(Foo.txt) = %v, want ErrExist", err)
	}
	// 任意大小写形式的查找都解析到同一目录项并核对 inode 身份。
	if raw := mustLookup(t, d, "FOO.TXT", 7, 3); raw != "Foo.txt" {
		t.Fatalf("resolved = %q", raw)
	}
	// 不同规范名不受影响。
	if err := d.Create("bar.txt", 10, 1); err != nil {
		t.Fatalf("Create bar.txt: %v", err)
	}
}

func TestNonASCIINames(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseFoldASCII)
	// 非 ASCII 字节不参与折叠："CAFÉ" 规范化为 "cafÉ"，与 "café" 不同，可共存。
	if err := d.Create("Café", 1, 1); err != nil {
		t.Fatalf("Create Café: %v", err)
	}
	if err := d.Create("CAFÉ", 2, 1); err != nil {
		t.Fatalf("Create CAFÉ: %v", err)
	}
	mustLookup(t, d, "café", 1, 1)
	mustLookup(t, d, "cafÉ", 2, 1)
	// 非 ASCII 名中的 ASCII 字母仍折叠："文件A" 与 "文件a" 冲突。
	if err := d.Create("文件A", 3, 1); err != nil {
		t.Fatalf("Create 文件A: %v", err)
	}
	if err := d.Create("文件a", 4, 1); err != ErrCanonicalConflict {
		t.Fatalf("Create(文件a) = %v, want ErrCanonicalConflict", err)
	}
	// À 不折叠，与 à 可共存。
	if err := d.Create("Àbc", 5, 1); err != nil {
		t.Fatalf("Create Àbc: %v", err)
	}
	if err := d.Create("àbc", 6, 1); err != nil {
		t.Fatalf("Create àbc: %v", err)
	}
}

func TestNameLengthLimits(t *testing.T) {
	h := newTestHost(t)
	p, err := NewPolicy(CaseFoldASCII, 8, 4)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	d, err := h.NewDirectory(p)
	if err != nil {
		t.Fatalf("NewDirectory: %v", err)
	}
	if err := d.Create("123456789", 1, 1); err != ErrNameTooLong {
		t.Fatalf("raw limit = %v, want ErrNameTooLong", err)
	}
	if err := d.Create("ABCDE", 1, 1); err != ErrNameTooLong {
		t.Fatalf("canonical limit = %v, want ErrNameTooLong", err)
	}
	if err := d.Create("ABCD", 1, 1); err != nil {
		t.Fatalf("Create ABCD: %v", err)
	}
}

// collidingMAC 是恒定的 HMAC 替身：所有名密钥与查找键都相同，
// 强制全部目录项落入同一查找键桶。
func collidingMAC(key, msg []byte) []byte {
	return bytes.Repeat([]byte{0x42}, 32)
}

func TestCollidingMACDigestCoexist(t *testing.T) {
	h := newTestHost(t, WithMACFunc(collidingMAC))
	d := newTestDir(t, h, CaseFoldASCII)
	names := []string{"alpha", "Beta", "gamma", "文件", "café"}
	for i, name := range names {
		if err := d.Create(name, uint64(100+i), 1); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	// 全部候选同处一桶，摘要碰撞可共存，查找仍须逐项解密核对。
	for i, name := range names {
		mustLookup(t, d, name, uint64(100+i), 1)
	}
	// 即使查找键相同，规范名冲突仍被拒绝。
	if err := d.Create("ALPHA", 200, 1); err != ErrCanonicalConflict {
		t.Fatalf("Create(ALPHA) = %v, want ErrCanonicalConflict", err)
	}
	if err := d.Create("alpha", 200, 1); err != ErrExist {
		t.Fatalf("Create(alpha) = %v, want ErrExist", err)
	}
}

func TestHardLinksSameInode(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	// 同一 inode 的两个硬链接：不同名字，相同 inode 身份。
	if err := d.Create("link-a", 42, 9); err != nil {
		t.Fatalf("Create link-a: %v", err)
	}
	if err := d.Create("link-b", 42, 9); err != nil {
		t.Fatalf("Create link-b: %v", err)
	}
	mustLookup(t, d, "link-a", 42, 9)
	mustLookup(t, d, "link-b", 42, 9)
	if err := d.Remove("link-a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, _, _, err := d.Lookup("link-a"); err != ErrNotFound {
		t.Fatalf("Lookup(link-a) = %v, want ErrNotFound", err)
	}
	mustLookup(t, d, "link-b", 42, 9)
}

func firstEntry(t *testing.T, d *Directory) *entry {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, bucket := range d.buckets {
		if len(bucket) > 0 {
			return bucket[0]
		}
	}
	t.Fatal("no entries")
	return nil
}

func TestLookupVerifiesIntegrity(t *testing.T) {
	t.Run("ciphertext tamper", func(t *testing.T) {
		d := newTestDir(t, newTestHost(t), CaseSensitive)
		if err := d.Create("victim", 1, 1); err != nil {
			t.Fatalf("Create: %v", err)
		}
		e := firstEntry(t, d)
		e.ciphertext[0] ^= 0x01
		if _, _, _, err := d.Lookup("victim"); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("Lookup = %v, want ErrIntegrity", err)
		}
	})
	t.Run("inode identity tamper", func(t *testing.T) {
		d := newTestDir(t, newTestHost(t), CaseSensitive)
		if err := d.Create("victim", 1, 1); err != nil {
			t.Fatalf("Create: %v", err)
		}
		e := firstEntry(t, d)
		e.inodeID++ // inode 身份被 AAD 绑定，篡改后解密必失败
		if _, _, _, err := d.Lookup("victim"); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("Lookup = %v, want ErrIntegrity", err)
		}
	})
}
