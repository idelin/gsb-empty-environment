package memfdseal

import (
	"errors"
	"syscall"
	"testing"
)

func mustOK(t *testing.T, err error, action string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: expected success, got %v", action, err)
	}
}

func wantErr(t *testing.T, err error, target error, action string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: expected %v, got %v", action, target, err)
	}
}

// 场景一：残留可写共享映射。F_SEAL_WRITE 必须 EBUSY 且整体不生效；
// munmap 之后才能封 WRITE，并满足交付条件。
func TestResidualWritableSharedMappingBlocksWriteSeal(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.Truncate(4096), "truncate")

	shared, err := m.Mmap(true, true, 4096)
	mustOK(t, err, "writable shared mmap")
	private, err := m.Mmap(true, false, 4096)
	mustOK(t, err, "writable private mmap")

	if !m.HasWritableSharedMapping() {
		t.Fatal("expected a live writable shared mapping")
	}

	// 加 SHRINK|GROW 可以成功，唯独 WRITE 因残留映射失败。
	mustOK(t, m.AddSeals(SealShrink|SealGrow), "add shrink/grow")
	wantErr(t, m.AddSeals(SealWrite), syscall.EBUSY, "add write while mapped")
	if m.Seals().Has(SealWrite) {
		t.Fatal("F_SEAL_WRITE must not take effect after EBUSY")
	}
	if m.Deliverable() {
		t.Fatal("memfd must not be deliverable while writable mapping is alive")
	}

	// 私有写时复制映射不影响底层文件，也不阻塞 WRITE。
	mustOK(t, m.MapWrite(private), "private map write")

	mustOK(t, m.Munmap(shared), "munmap shared")
	if m.HasWritableSharedMapping() {
		t.Fatal("writable shared mapping should be gone after munmap")
	}

	mustOK(t, m.AddSeals(SealWrite), "add write after munmap")
	if !m.Seals().Has(SealWrite | SealShrink | SealGrow) {
		t.Fatalf("F_SEAL_WRITE must imply SHRINK|GROW, got %s", m.Seals())
	}
	mustOK(t, m.AddSeals(SealSeal), "add seal")
	if !m.Deliverable() {
		t.Fatalf("expected deliverable, blocked: %v", m.BlockedReasons())
	}
}

// 场景二：仅 F_SEAL_FUTURE_WRITE。设置之后新 write/新共享可写 mmap 被拒，
// 但设置之前已存在的可写共享映射仍可改写内容，因此不满足写后封印与交付条件。
func TestFutureWriteKeepsExistingMappingsWritable(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.Truncate(4096), "truncate")

	shared, err := m.Mmap(true, true, 4096)
	mustOK(t, err, "writable shared mmap before future-write")
	before := m.ContentVersion()

	mustOK(t, m.AddSeals(SealFutureWrite), "add future write")
	if !m.Seals().Has(SealFutureWrite | SealShrink | SealGrow) {
		t.Fatalf("FUTURE_WRITE must imply SHRINK|GROW, got %s", m.Seals())
	}

	// 新 write 被 EPERM 拒绝，新建可写共享映射被 EACCES 拒绝。
	wantErr(t, m.Write([]byte("x")), syscall.EPERM, "write after future-write")
	_, err = m.Mmap(true, true, 128)
	wantErr(t, err, syscall.EACCES, "new writable shared mmap after future-write")

	// 已有映射仍然可写，文件内容继续变化——这正是写后封印缺口。
	mustOK(t, m.MapWrite(shared), "write through existing shared mapping")
	if m.ContentVersion() != before+1 {
		t.Fatalf("existing mapping write must mutate file, version before=%d after=%d", before, m.ContentVersion())
	}

	if m.Deliverable() {
		t.Fatal("F_SEAL_FUTURE_WRITE alone must never satisfy delivery")
	}
	for _, reason := range m.BlockedReasons() {
		if reason == "missing seal F_SEAL_WRITE" {
			return
		}
	}
	t.Fatalf("expected missing F_SEAL_WRITE in reasons: %v", m.BlockedReasons())
}

// 场景三：先封 F_SEAL_SEAL，之后任何加封印尝试都必须 EPERM，
// 且交付条件因缺少 WRITE/SHRINK/GROW 而不满足。
func TestSealSealLocksFurtherSealing(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.AddSeals(SealSeal), "add seal-seal first")

	wantErr(t, m.AddSeals(SealWrite), syscall.EPERM, "add write after seal-seal")
	wantErr(t, m.AddSeals(SealShrink), syscall.EPERM, "add shrink after seal-seal")

	if m.Seals() != SealSeal {
		t.Fatalf("no further seal may take effect, got %s", m.Seals())
	}
	if m.Deliverable() {
		t.Fatal("seal-seal alone must not satisfy delivery")
	}
	if _, err := m.Deliver(); !errors.Is(err, ErrNotDeliverable) {
		t.Fatalf("Deliver expected ErrNotDeliverable, got %v", err)
	}
	if m.DupCount() != 0 {
		t.Fatal("failed Deliver must not hand out a descriptor")
	}
}

// 场景四：合规序列——ftruncate、write、私有映射、在无任何可写共享映射时
// 依次补齐四封印，Deliver 成功并真正完成 dup 移交。
func TestCompliantSequenceDelivers(t *testing.T) {
	m := New(Config{AllowSealing: true})
	ops := []Op{
		TruncateOp{Size: 8192},
		WriteOp{Data: []byte("sandbox-policy-v1")},
		MmapOp{Writable: false, Shared: true, Length: 8192}, // 只读共享映射
		MmapOp{Writable: true, Shared: false, Length: 8192}, // 私有写时复制
		AddSealsOp{Seals: SealWrite},
		AddSealsOp{Seals: SealSeal},
		DeliverOp{},
	}
	report := m.Run(ops)

	for _, step := range report.Steps {
		if step.Err != nil {
			t.Fatalf("step %d (%v) unexpectedly failed: %v", step.Index, step.Op, step.Err)
		}
	}
	if !report.Deliverable {
		t.Fatalf("expected deliverable, reasons: %v", report.BlockedReasons)
	}
	if !report.Seals.Has(SealWrite | SealShrink | SealGrow | SealSeal) {
		t.Fatalf("all four seals required, got %s", report.Seals)
	}
	if m.DupCount() != 1 {
		t.Fatalf("expected exactly one delivered descriptor, got %d", m.DupCount())
	}
	if m.HasWritableSharedMapping() {
		t.Fatal("read-only shared and private mappings must not count as writable shared")
	}

	// 封印完成后内容被写后固定：write 与 ftruncate 全部 EPERM。
	wantErr(t, m.Write([]byte("tamper")), syscall.EPERM, "post-seal write")
	wantErr(t, m.Truncate(4096), syscall.EPERM, "post-seal shrink")
	wantErr(t, m.Truncate(16384), syscall.EPERM, "post-seal grow")
}

// 创建时未带 MFD_ALLOW_SEALING 的 memfd 永远不可封印。
func TestMissingAllowSealingRejectsAllSeals(t *testing.T) {
	m := New(Config{AllowSealing: false})
	mustOK(t, m.Truncate(128), "truncate")

	wantErr(t, m.AddSeals(SealSeal), syscall.EPERM, "seal-seal without MFD_ALLOW_SEALING")
	wantErr(t, m.AddSeals(SealWrite), syscall.EPERM, "write without MFD_ALLOW_SEALING")
	if m.Seals() != 0 {
		t.Fatalf("no seal may take effect, got %s", m.Seals())
	}
}

// SHRINK/GROW 对 ftruncate 与 write 越界扩张的拦截，以及未知封印位 EINVAL。
func TestShrinkGrowAndInvalidSealBits(t *testing.T) {
	m := New(Config{AllowSealing: true, InitialSize: 1024})
	mustOK(t, m.AddSeals(SealShrink), "add shrink")
	wantErr(t, m.Truncate(512), syscall.EPERM, "shrink")
	mustOK(t, m.Truncate(2048), "grow allowed without grow seal")

	mustOK(t, m.AddSeals(SealGrow), "add grow")
	wantErr(t, m.Truncate(4096), syscall.EPERM, "grow")

	m2 := New(Config{AllowSealing: true})
	mustOK(t, m2.Truncate(4), "small file")
	mustOK(t, m2.AddSeals(SealGrow), "m2 add grow")
	wantErr(t, m2.Write(make([]byte, 8)), syscall.EPERM, "write extending beyond size")

	wantErr(t, m2.AddSeals(SealSet(1<<31)), syscall.EINVAL, "unknown seal bit")
	wantErr(t, m2.Munmap(99), syscall.EINVAL, "munmap unknown id")
}

// Run 在中途操作失败时继续执行后续操作，并如实记录每一步与最终判定。
func TestRunContinuesAfterFailedStep(t *testing.T) {
	m := New(Config{AllowSealing: true})
	report := m.Run([]Op{
		TruncateOp{Size: 4096},
		MmapOp{Writable: true, Shared: true, Length: 4096},
		AddSealsOp{Seals: SealWrite}, // EBUSY，失败但序列继续
		DupOp{},                      // 无门禁 dup，仍然计数
	})
	if len(report.Steps) != 4 {
		t.Fatalf("expected 4 step results, got %d", len(report.Steps))
	}
	if !errors.Is(report.Steps[2].Err, syscall.EBUSY) {
		t.Fatalf("step 2 expected EBUSY, got %v", report.Steps[2].Err)
	}
	if report.Deliverable {
		t.Fatal("expected non-deliverable report")
	}
	if m.DupCount() != 1 {
		t.Fatalf("unguarded Dup should still count, got %d", m.DupCount())
	}
	if report.Seals.String() != "NONE" {
		t.Fatalf("failed seal call must leave no seals, got %s", report.Seals)
	}
}
