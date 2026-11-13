package netsec

import "testing"

func TestRotate(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseFoldASCII)
	names := []string{"Alpha", "beta", "Gamma.txt", "文件1"}
	for i, name := range names {
		if err := d.Create(name, uint64(10+i), 1); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	if err := d.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if got := d.Policy().KeyGeneration; got != 2 {
		t.Fatalf("KeyGeneration = %d, want 2", got)
	}
	for i, name := range names {
		mustLookup(t, d, name, uint64(10+i), 1)
	}
	// 轮换后仍可创建与删除。
	if err := d.Create("delta", 99, 1); err != nil {
		t.Fatalf("Create after rotate: %v", err)
	}
	mustLookup(t, d, "DELTA", 99, 1)
}

func TestRotationInterrupted(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	names := []string{"a", "b", "c", "d", "e", "f"}
	for i, name := range names {
		if err := d.Create(name, uint64(1+i), 1); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	if err := d.BeginRotation(); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	// 模拟中断：只迁移一部分目录项。
	for i := 0; i < 3; i++ {
		if _, err := d.RotateOne(); err != nil {
			t.Fatalf("RotateOne: %v", err)
		}
	}
	// 混合代次状态下全部查找（解密并核对 inode）仍须成功。
	for i, name := range names {
		mustLookup(t, d, name, uint64(1+i), 1)
	}
	// 中断时旧代次未撤下，FinishRotation 必须拒绝。
	if err := d.FinishRotation(); err != ErrRotationIncomplete {
		t.Fatalf("FinishRotation = %v, want ErrRotationIncomplete", err)
	}
	// 恢复轮换直至完成。
	for {
		remaining, err := d.RotateOne()
		if err != nil {
			t.Fatalf("RotateOne: %v", err)
		}
		if remaining == 0 {
			break
		}
	}
	if err := d.FinishRotation(); err != nil {
		t.Fatalf("FinishRotation: %v", err)
	}
	for i, name := range names {
		mustLookup(t, d, name, uint64(1+i), 1)
	}
	if got := d.Policy().KeyGeneration; got != 2 {
		t.Fatalf("KeyGeneration = %d, want 2", got)
	}
}

func TestRotationGuards(t *testing.T) {
	d := newTestDir(t, newTestHost(t), CaseSensitive)
	if _, err := d.RotateOne(); err != ErrNoRotation {
		t.Fatalf("RotateOne = %v, want ErrNoRotation", err)
	}
	if err := d.FinishRotation(); err != ErrNoRotation {
		t.Fatalf("FinishRotation = %v, want ErrNoRotation", err)
	}
	if err := d.BeginRotation(); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if err := d.BeginRotation(); err != ErrRotationActive {
		t.Fatalf("BeginRotation = %v, want ErrRotationActive", err)
	}
}

// 轮换期间发生碰撞替身时，新旧代次目录项同桶，查找仍须正确。
func TestRotationWithCollidingMAC(t *testing.T) {
	h := newTestHost(t, WithMACFunc(collidingMAC))
	d := newTestDir(t, h, CaseFoldASCII)
	names := []string{"one", "Two", "three"}
	for i, name := range names {
		if err := d.Create(name, uint64(50+i), 1); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	if err := d.BeginRotation(); err != nil {
		t.Fatalf("BeginRotation: %v", err)
	}
	if _, err := d.RotateOne(); err != nil {
		t.Fatalf("RotateOne: %v", err)
	}
	for i, name := range names {
		mustLookup(t, d, name, uint64(50+i), 1)
	}
	for {
		remaining, err := d.RotateOne()
		if err != nil {
			t.Fatalf("RotateOne: %v", err)
		}
		if remaining == 0 {
			break
		}
	}
	if err := d.FinishRotation(); err != nil {
		t.Fatalf("FinishRotation: %v", err)
	}
	for i, name := range names {
		mustLookup(t, d, name, uint64(50+i), 1)
	}
}
