package netsec

import "testing"

func TestRenameCrossDirectory(t *testing.T) {
	h := newTestHost(t)
	src := newTestDir(t, h, CaseSensitive)
	dst := newTestDir(t, h, CaseFoldASCII)

	if err := src.Create("Report.TXT", 7, 3); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 跨目录 rename：解密后按目标策略重新封装，inode 身份保持不变。
	if err := Rename(src, "Report.TXT", dst, "summary.TXT"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, _, _, err := src.Lookup("Report.TXT"); err != ErrNotFound {
		t.Fatalf("src Lookup = %v, want ErrNotFound", err)
	}
	if raw := mustLookup(t, dst, "SUMMARY.txt", 7, 3); raw != "summary.TXT" {
		t.Fatalf("resolved = %q", raw)
	}
}

func TestRenameSameDirectory(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseFoldASCII)
	if err := d.Create("Old.Name", 5, 2); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Rename(d, "old.name", d, "New.Name"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, _, _, err := d.Lookup("old.name"); err != ErrNotFound {
		t.Fatalf("Lookup(old) = %v, want ErrNotFound", err)
	}
	mustLookup(t, d, "NEW.name", 5, 2)
}

func TestRenameFailureKeepsSource(t *testing.T) {
	h := newTestHost(t)
	src := newTestDir(t, h, CaseSensitive)
	dst := newTestDir(t, h, CaseFoldASCII)

	if err := src.Create("Report.TXT", 7, 3); err != nil {
		t.Fatalf("Create src: %v", err)
	}
	if err := dst.Create("existing.txt", 8, 1); err != nil {
		t.Fatalf("Create dst: %v", err)
	}
	// 目标规范名冲突：rename 失败且源项不移除。
	if err := Rename(src, "Report.TXT", dst, "EXISTING.TXT"); err != ErrCanonicalConflict {
		t.Fatalf("Rename = %v, want ErrCanonicalConflict", err)
	}
	mustLookup(t, src, "Report.TXT", 7, 3)
	mustLookup(t, dst, "existing.txt", 8, 1)

	// 目标名非法：同样不动源项。
	if err := Rename(src, "Report.TXT", dst, "bad/name"); err != ErrInvalidName {
		t.Fatalf("Rename = %v, want ErrInvalidName", err)
	}
	mustLookup(t, src, "Report.TXT", 7, 3)
}

func TestRenameExchange(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	if err := d.Create("alpha", 1, 1); err != nil {
		t.Fatalf("Create alpha: %v", err)
	}
	if err := d.Create("beta", 2, 1); err != nil {
		t.Fatalf("Create beta: %v", err)
	}
	if err := d.Exchange("alpha", "beta"); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	// 名字互换，inode 身份随原目录项。
	mustLookup(t, d, "alpha", 2, 1)
	mustLookup(t, d, "beta", 1, 1)
}

func TestExchangeFoldCasePreservesRaw(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseFoldASCII)
	if err := d.Create("Foo.txt", 1, 1); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := d.Create("Bar.txt", 2, 1); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 用不同大小写形式寻址；交换后名字槽位保留原始名，inode 身份互换。
	if err := d.Exchange("FOO.TXT", "BAR.TXT"); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if raw := mustLookup(t, d, "foo.txt", 2, 1); raw != "Foo.txt" {
		t.Fatalf("resolved = %q, want Foo.txt", raw)
	}
	if raw := mustLookup(t, d, "bar.txt", 1, 1); raw != "Bar.txt" {
		t.Fatalf("resolved = %q, want Bar.txt", raw)
	}
}
