package memfdseal

import (
	"errors"
	"strings"
	"syscall"
	"testing"
)

func TestAccessorsAndStringer(t *testing.T) {
	m := New(Config{AllowSealing: true, InitialSize: 16})
	if m.Size() != 16 || m.Offset() != 0 || m.Seals() != 0 || m.ContentVersion() != 0 {
		t.Fatalf("unexpected initial state: size=%d offset=%d seals=%s", m.Size(), m.Offset(), m.Seals())
	}
	if SealSet(0).String() != "NONE" {
		t.Fatal("empty seal set must render NONE")
	}
	mustOK(t, m.AddSeals(SealWrite|SealSeal), "add write+seal")
	rendered := m.Seals().String()
	for _, name := range []string{"F_SEAL_SEAL", "F_SEAL_SHRINK", "F_SEAL_GROW", "F_SEAL_WRITE"} {
		if !strings.Contains(rendered, name) {
			t.Fatalf("rendered seals %q missing %s", rendered, name)
		}
	}
	if strings.Contains(rendered, "F_SEAL_FUTURE_WRITE") {
		t.Fatalf("FUTURE_WRITE should not appear in %q", rendered)
	}
}

func TestMappingSnapshotAndEdgeErrors(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.Truncate(128), "truncate")

	readOnly, err := m.Mmap(false, true, 64)
	mustOK(t, err, "read-only shared mmap")
	private, err := m.Mmap(true, false, 64)
	mustOK(t, err, "writable private mmap")

	snapshot := m.Mappings()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 live mappings, got %d", len(snapshot))
	}
	snapshot[0].Length = 0 // 修改快照不应影响状态机内部
	if m.Mappings()[0].Length == 0 {
		t.Fatal("Mappings must return a defensive copy")
	}

	wantErr(t, m.MapWrite(readOnly), ErrReadOnlyMapping, "write via read-only mapping")

	// 私有可写映射成功但属于写时复制，不增加文件内容版本。
	before := m.ContentVersion()
	mustOK(t, m.MapWrite(private), "private mapping write")
	if m.ContentVersion() != before {
		t.Fatal("MAP_PRIVATE write must not mutate the backing file")
	}

	wantErr(t, m.MapWrite(12345), syscall.EINVAL, "map write unknown id")
	wantErr(t, m.Munmap(12345), syscall.EINVAL, "munmap unknown id")
	_, err = m.Mmap(true, true, 0)
	wantErr(t, err, syscall.EINVAL, "zero-length mmap")
	wantErr(t, m.Truncate(-1), syscall.EINVAL, "negative truncate")
}

func TestTruncateClampsOffset(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.Truncate(1024), "truncate")
	mustOK(t, m.Write(make([]byte, 512)), "write 512 bytes")
	if m.Offset() != 512 {
		t.Fatalf("offset should be 512, got %d", m.Offset())
	}
	mustOK(t, m.Truncate(256), "shrink below offset")
	if m.Offset() != 256 {
		t.Fatalf("offset should be clamped to 256, got %d", m.Offset())
	}
}

func TestNewIgnoresNegativeInitialSize(t *testing.T) {
	m := New(Config{AllowSealing: false, InitialSize: -9})
	if m.Size() != 0 {
		t.Fatalf("negative initial size must normalize to 0, got %d", m.Size())
	}
	if m.Deliverable() || !errors.Is(deliverErr(m), ErrNotDeliverable) {
		t.Fatal("unsealed memfd must not be deliverable")
	}
}

func deliverErr(m *Machine) error {
	_, err := m.Deliver()
	return err
}

func TestMmapOpStringer(t *testing.T) {
	rendered := MmapOp{Writable: true, Shared: false, Length: 4096}.String()
	if !strings.Contains(rendered, "mmap(") || !strings.Contains(rendered, "4096") {
		t.Fatalf("unexpected MmapOp rendering: %q", rendered)
	}
}

func TestWriteGrowthBlockedAndOpAdapters(t *testing.T) {
	m := New(Config{AllowSealing: true})
	mustOK(t, m.Truncate(4), "truncate to 4")
	mustOK(t, m.AddSeals(SealGrow), "seal grow")
	wantErr(t, m.Write(make([]byte, 8)), syscall.EPERM, "write must not grow sealed file")

	// 在已有尺寸内写入不触发扩张逻辑，应成功并前移偏移。
	within := New(Config{AllowSealing: true, InitialSize: 16})
	mustOK(t, within.Write(make([]byte, 4)), "in-bounds write")
	if within.Offset() != 4 || within.Size() != 16 || within.ContentVersion() != 1 {
		t.Fatalf("in-bounds write state wrong: offset=%d size=%d version=%d",
			within.Offset(), within.Size(), within.ContentVersion())
	}
	// 即使带 F_SEAL_GROW，不越界的写入也应成功（覆盖内写分支）。
	mustOK(t, within.AddSeals(SealGrow), "add grow to in-bounds file")
	mustOK(t, within.Write(make([]byte, 4)), "in-bounds write under grow seal")
	if within.Offset() != 8 {
		t.Fatalf("offset should advance to 8, got %d", within.Offset())
	}

	// 无 GROW 封印时，write 越界会自动扩张文件大小。
	growing := New(Config{AllowSealing: true, InitialSize: 4})
	mustOK(t, growing.Write(make([]byte, 10)), "growing write")
	if growing.Size() != 10 || growing.Offset() != 10 {
		t.Fatalf("expected size/offset 10, got size=%d offset=%d", growing.Size(), growing.Offset())
	}

	// 通过 Op 接口驱动时，MmapOp 回填 ID，MunmapOp 能消费该 ID。
	report := m.Run([]Op{
		MmapOp{Writable: false, Shared: true, Length: 4},
		MunmapOp{MappingID: 0}, // 覆盖 MunmapOp.apply 成功路径
	})
	for _, step := range report.Steps {
		if step.Err != nil {
			t.Fatalf("step %d failed: %v", step.Index, step.Err)
		}
	}
	if report.Steps[0].Op.(MmapOp).MappingID != 0 {
		t.Fatal("MmapOp should record the allocated mapping id")
	}

	// 直接命中 New 内部的负尺寸归一化分支与 MapWriteOp.apply。
	small := New(Config{InitialSize: -1})
	if small.Size() != 0 {
		t.Fatalf("expected normalized zero size, got %d", small.Size())
	}
	id, err := small.Mmap(true, true, 8)
	mustOK(t, err, "shared mapping for MapWriteOp")
	mapReport := small.Run([]Op{MapWriteOp{MappingID: id}})
	if mapReport.Steps[0].Err != nil {
		t.Fatalf("MapWriteOp failed: %v", mapReport.Steps[0].Err)
	}
}
