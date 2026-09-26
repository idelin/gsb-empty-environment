package gsb

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func spliceModel(m []byte, off, remove int, data []byte) []byte {
	out := append([]byte(nil), m[:off]...)
	out = append(out, data...)
	return append(out, m[off+remove:]...)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRandomOpsVsNaive 用朴素字节串模型对拍随机操作序列：
// 插入/覆盖/删除/移动/快照/删分支/取消回滚/区间读/跨块整数读，
// 每个操作后独立遍历存活引用核对账本。
func TestRandomOpsVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260926))
	r := NewRepo(0)
	model := map[string][]byte{"main": {}}
	live := []string{"main"}
	names := []string{"main", "b1", "b2", "b3"}

	randBytes := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}

	for i := 0; i < 4000; i++ {
		br := live[rng.Intn(len(live))]
		m := model[br]
		op := rng.Intn(100)
		switch {
		case op < 22: // 插入
			off := rng.Intn(len(m) + 1)
			data := randBytes(rng.Intn(180))
			if err := r.Insert(br, off, data); err != nil {
				t.Fatalf("op %d Insert: %v", i, err)
			}
			model[br] = spliceModel(m, off, 0, data)
		case op < 42: // 覆盖
			if len(m) == 0 {
				continue
			}
			off := rng.Intn(len(m))
			n := 1 + rng.Intn(min(120, len(m)-off))
			data := randBytes(n)
			if err := r.Overwrite(br, off, data); err != nil {
				t.Fatalf("op %d Overwrite: %v", i, err)
			}
			model[br] = spliceModel(m, off, n, data)
		case op < 56: // 删除
			if len(m) == 0 {
				continue
			}
			off := rng.Intn(len(m))
			n := 1 + rng.Intn(min(120, len(m)-off))
			if err := r.Delete(br, off, n); err != nil {
				t.Fatalf("op %d Delete: %v", i, err)
			}
			model[br] = spliceModel(m, off, n, nil)
		case op < 70: // 移动（含重叠区间）
			if len(m) == 0 {
				continue
			}
			src := rng.Intn(len(m))
			n := 1 + rng.Intn(min(100, len(m)-src))
			dst := rng.Intn(len(m) + 1)
			if err := r.Move(br, src, dst, n); err != nil {
				t.Fatalf("op %d Move(%d,%d,%d) len=%d: %v", i, src, dst, n, len(m), err)
			}
			model[br] = modelMove(m, src, dst, n)
		case op < 78: // 分支快照
			dst := names[rng.Intn(len(names))]
			if err := r.Snapshot(dst, br); err != nil {
				t.Fatalf("op %d Snapshot: %v", i, err)
			}
			model[dst] = append([]byte(nil), m...)
			if !contains(live, dst) {
				live = append(live, dst)
			}
		case op < 83: // 删除分支
			if len(live) <= 1 {
				continue
			}
			idx := rng.Intn(len(live))
			victim := live[idx]
			if err := r.DropBranch(victim); err != nil {
				t.Fatalf("op %d DropBranch: %v", i, err)
			}
			delete(model, victim)
			live = append(live[:idx], live[idx+1:]...)
		case op < 89: // 事务内编辑后取消，整体回滚
			tx := r.Begin()
			off := rng.Intn(len(m) + 1)
			data := randBytes(rng.Intn(120))
			if err := r.Splice(tx, br, off, 0, data); err != nil {
				t.Fatalf("op %d tx Splice: %v", i, err)
			}
			if err := r.SnapshotTx(tx, "tmp", br); err != nil {
				t.Fatalf("op %d tx Snapshot: %v", i, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("op %d Rollback: %v", i, err)
			}
			if _, ok := r.branches["tmp"]; ok {
				t.Fatalf("op %d 回滚后临时分支仍存在", i)
			}
		case op < 95: // 随机区间读对拍
			if len(m) == 0 {
				continue
			}
			off := rng.Intn(len(m))
			n := rng.Intn(len(m) - off + 1)
			got, err := r.Read(br, off, n)
			if err != nil {
				t.Fatalf("op %d Read: %v", i, err)
			}
			if !bytes.Equal(got, m[off:off+n]) {
				t.Fatalf("op %d Read(%q,%d,%d) 与模型不符", i, br, off, n)
			}
		default: // 跨块整数读端序对拍
			if len(m) < 8 {
				continue
			}
			off := rng.Intn(len(m) - 7)
			vLE, err := r.ReadUint64LE(br, off)
			if err != nil {
				t.Fatalf("op %d ReadUint64LE: %v", i, err)
			}
			if want := binary.LittleEndian.Uint64(m[off:]); vLE != want {
				t.Fatalf("op %d 小端读取 = %#x，期望 %#x", i, vLE, want)
			}
			vBE, err := r.ReadUint64BE(br, off)
			if err != nil {
				t.Fatalf("op %d ReadUint64BE: %v", i, err)
			}
			if want := binary.BigEndian.Uint64(m[off:]); vBE != want {
				t.Fatalf("op %d 大端读取 = %#x，期望 %#x", i, vBE, want)
			}
		}
		if err := r.VerifyLedger(); err != nil {
			t.Fatalf("op %d 账本核对失败: %v", i, err)
		}
		if i%97 == 0 {
			r.Store().Collect()
		}
		if i%113 == 0 {
			for name, want := range model {
				got, err := r.ReadAll(name)
				if err != nil {
					t.Fatalf("op %d ReadAll(%q): %v", i, name, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("op %d 分支 %q 全量对拍失败: 长 %d vs %d", i, name, len(got), len(want))
				}
			}
		}
	}
	// 收尾：全量对拍 + 账本核对 + 回收后再核对。
	for name, want := range model {
		got, err := r.ReadAll(name)
		if err != nil {
			t.Fatalf("final ReadAll(%q): %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("final 分支 %q 与模型不符", name)
		}
	}
	r.Store().Collect()
	if err := r.VerifyLedger(); err != nil {
		t.Fatalf("final 账本核对失败: %v", err)
	}
}
