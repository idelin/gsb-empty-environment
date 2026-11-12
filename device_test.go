package netsec

import (
	"errors"
	"testing"
)

// TestMemoryDeviceLifecycle 验证小块设备的分配、读取、释放、双重分配
// 与越界访问语义，保证逐块验证建立在可信的块层之上。
func TestMemoryDeviceLifecycle(t *testing.T) {
	device := NewMemoryDevice(4, 8)

	if device.TotalBlocks() != 4 || device.BlockSize() != 8 {
		t.Fatalf("设备几何参数错误: blocks=%d size=%d", device.TotalBlocks(), device.BlockSize())
	}
	if device.IsAllocated(0) {
		t.Fatal("新设备块不应处于已分配状态")
	}
	if _, err := device.Read(0); !errors.Is(err, ErrBlockFree) {
		t.Fatalf("读未分配块应返回 ErrBlockFree，实际: %v", err)
	}
	if err := device.Allocate(4, nil); !errors.Is(err, ErrBlockOutOfRange) {
		t.Fatalf("越界分配应返回 ErrBlockOutOfRange，实际: %v", err)
	}

	if err := device.Allocate(1, []byte("payload")); err != nil {
		t.Fatalf("分配块 1: %v", err)
	}
	if err := device.Allocate(1, []byte("again")); err == nil {
		t.Fatal("重复分配应报错而不是覆盖证据")
	}
	data, err := device.Read(1)
	if err != nil || string(data[:7]) != "payload" {
		t.Fatalf("块内容应保持首次写入，实际: %q err=%v", data, err)
	}

	if err := device.Free(1); err != nil {
		t.Fatalf("释放块 1: %v", err)
	}
	if err := device.Free(1); !errors.Is(err, ErrBlockFree) {
		t.Fatalf("重复释放应返回 ErrBlockFree，实际: %v", err)
	}
}

// TestVerifyBlockReferencesOutOfRange 验证越界区段引用被显式标记，
// 而不是被静默吞掉。
func TestVerifyBlockReferencesOutOfRange(t *testing.T) {
	device := NewMemoryDevice(2, 4)
	mustAllocate(t, device, 0)
	table := &SegmentTable{Objects: []Object{
		{Ref: ref(1, 1), Extents: []Extent{{0, 1}, {5, 1}}},
	}}

	accounts, err := VerifyBlockReferences(device, table)
	if !errors.Is(err, ErrBlockOutOfRange) {
		t.Fatalf("越界引用应返回 ErrBlockOutOfRange，实际: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Block != 0 {
		t.Fatalf("范围内块仍应完成记账，实际: %+v", accounts)
	}
}

// TestBitmapAdapter 验证 Bitmap 适配器的越界与正常判定。
func TestBitmapAdapter(t *testing.T) {
	bitmap := Bitmap{false, true, false}
	if bitmap.TotalBlocks() != 3 {
		t.Fatalf("位图块数应为 3，实际: %d", bitmap.TotalBlocks())
	}
	if !bitmap.IsAllocated(1) || bitmap.IsAllocated(0) || bitmap.IsAllocated(99) {
		t.Fatal("位图判定不符合预期，越界块必须返回 false")
	}
}
