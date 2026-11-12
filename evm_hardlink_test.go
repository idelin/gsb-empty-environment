package netsec

import (
	"os"
	"path/filepath"
	"testing"
)

// 把文件 A 的合法 HMAC 原样拷到另一个 inode 的文件 B（复制攻击）：
// 属性值相同但 inode 不同，全量 HMAC 不匹配；用签发期 inode 记录重算
// 仍能命中，结论必须是 inode-mismatch 而非属性篡改。
func TestHMAC_CopiedToAnotherInodeIsInodeMismatch(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	src := baseMeta()
	src.EVMAttr = mustHMACAttr(t, src)

	cases := map[string]FileMeta{
		"both-changed": {
			Path: "/etc/app/config.copy", Ino: 2002, Generation: 9,
		},
		"inode-only-changed": {
			Path: "/etc/app/config.copy", Ino: 2002, Generation: src.Generation,
		},
		"generation-only-changed": {
			Path: "/etc/app/config.copy", Ino: src.Ino, Generation: 99,
		},
	}
	for name, dest := range cases {
		t.Run(name, func(t *testing.T) {
			copyFile := src
			copyFile.Path = dest.Path
			copyFile.Ino, copyFile.Generation = dest.Ino, dest.Generation
			// 引擎记录的签发期 inode 绑定（来自安全清单）。
			copyFile.EVMBoundIno = src.Ino
			copyFile.EVMBoundGeneration = src.Generation
			// EVMAttr 原样保留，模拟把 security.evm 一起拷贝过来
			assertResult(t, eng.Evaluate(copyFile), StatusDeny, ReasonInodeMismatch)
		})
	}
}

// 没有签发期 inode 记录时，跨 inode 复制无法与属性篡改区分，
// 保守结论是属性不匹配（仍然拒绝，绝不放行）。
func TestHMAC_CopiedWithoutBindingRecordFallsBackToAttrMismatch(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	src := baseMeta()
	src.EVMAttr = mustHMACAttr(t, src)

	copyFile := src
	copyFile.Path = "/etc/app/config.copy"
	copyFile.Ino, copyFile.Generation = 2002, 9
	// EVMBoundIno 留零：无签发期记录

	assertResult(t, eng.Evaluate(copyFile), StatusDeny, ReasonAttrMismatch)
}

// 硬链接到另一路径但 inode 相同：规范输入只含 inode/generation 不含路径，
// 因此必须通过，且不能误报为复制。
func TestHMAC_HardLinkSameInodePasses(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	original := baseMeta()
	original.EVMAttr = mustHMACAttr(t, original)

	hardLink := original
	hardLink.Path = "/etc/app/hardlink-config"
	// Ino、Generation 保持不变

	assertResult(t, eng.Evaluate(hardLink), StatusPass, ReasonHMACValid)
}

// 真实文件系统上的硬链接：两条路径 stat 得到同一 inode，评估结论一致且通过。
func TestHMAC_RealFilesystemHardLink(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a")
	bPath := filepath.Join(dir, "b")
	if err := os.WriteFile(aPath, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.Link(aPath, bPath); err != nil {
		t.Skipf("hard links unsupported on this filesystem: %v", err)
	}

	stA, err := os.Stat(aPath)
	if err != nil {
		t.Fatalf("stat a: %v", err)
	}
	stB, err := os.Stat(bPath)
	if err != nil {
		t.Fatalf("stat b: %v", err)
	}
	if !os.SameFile(stA, stB) {
		t.Fatal("hard link must resolve to the same file identity")
	}

	ino, gen := statInoGen(stA)
	inoB, genB := statInoGen(stB)
	if ino != inoB || gen != genB {
		t.Fatalf("hard link inode mismatch: (%d,%d) vs (%d,%d)", ino, gen, inoB, genB)
	}

	eng, _ := newTestEngine(t, false)
	meta := FileMeta{
		Path:       aPath,
		Ino:        ino,
		Generation: gen,
		UID:        statUID(stA),
		GID:        statGID(stA),
		Mode:       uint32(stA.Mode().Perm()),
		IMA:        []byte("real-fs-ima"),
		SELinux:    []byte("real-fs-selinux\x00"),
	}
	meta.EVMAttr = mustHMACAttr(t, meta)

	linked := meta
	linked.Path = bPath // 同 inode、同 generation、同属性，仅路径不同

	assertResult(t, eng.Evaluate(meta), StatusPass, ReasonHMACValid)
	assertResult(t, eng.Evaluate(linked), StatusPass, ReasonHMACValid)
}

// 主机密钥未加载：类型 2 一律拒绝，即便 security.ima 存在且合法，
// 也不允许退回只检查 IMA。
func TestHMAC_MissingHostKeyAlwaysDeniesNoIMAFallback(t *testing.T) {
	eng := Engine{} // 无任何密钥
	if eng.KeysLoaded() {
		t.Fatal("fresh engine should report no loaded keys")
	}

	meta := baseMeta()
	meta.IMA = []byte("perfectly-valid-ima-digest")
	meta.EVMAttr = mustHMACAttr(t, meta) // 由别处生成的合法 HMAC

	assertResult(t, eng.Evaluate(meta), StatusDeny, ReasonNoKey)
}
