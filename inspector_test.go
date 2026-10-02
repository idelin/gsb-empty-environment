package checksumdiff

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func makeRow(id int64) Row {
	return Row{ID: id, Name: fmt.Sprintf("user-%d", id), Balance: id * 100, UpdatedAt: id, Version: 1}
}

// loadStores 向两个库写入相同的 n 行。
func loadStores(n int) (*Store, *Store) {
	src, dst := NewStore("source"), NewStore("target")
	for i := 0; i < n; i++ {
		r := makeRow(int64(i))
		src.Upsert(r)
		dst.Upsert(r)
	}
	return src, dst
}

func TestInspectEqualStores(t *testing.T) {
	src, dst := loadStores(20000)
	rep := NewInspector(src, dst).Inspect()
	if !rep.Equal {
		t.Fatalf("两库一致但报告不相等: %+v", rep.AllDiffs())
	}
	if rep.SrcRoot != rep.DstRoot {
		t.Fatal("一致的两库根签名应相同")
	}
}

func TestDetectSingleFieldTamper(t *testing.T) {
	src, dst := loadStores(50000)
	const victimID = 12345
	row, _ := dst.Get(victimID)
	row.Balance++ // 人为篡改目标库单个字段
	dst.Upsert(row)

	rep := NewInspector(src, dst).Inspect()
	if rep.Equal {
		t.Fatal("篡改后巡检未发现不一致")
	}
	diffs := rep.AllDiffs()
	if len(diffs) != 1 {
		t.Fatalf("应只定位到 1 行差异，实际 %d: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.ID != victimID || d.Kind != Modified {
		t.Fatalf("差异定位错误: %+v", d)
	}
	if d.Target.Balance != d.Source.Balance+1 {
		t.Fatalf("应能还原篡改前后内容: %+v", d)
	}
	t.Logf("精确定位: bucket=%d pk=%d kind=%s src.Balance=%d dst.Balance=%d",
		rep.Buckets[0].Bucket, d.ID, d.Kind, d.Source.Balance, d.Target.Balance)
}

func TestDetectMissingRow(t *testing.T) {
	src, dst := loadStores(10000)
	dst.Delete(777)
	rep := NewInspector(src, dst).Inspect()
	diffs := rep.AllDiffs()
	if len(diffs) != 1 || diffs[0].ID != 777 || diffs[0].Kind != MissingInTarget {
		t.Fatalf("丢数检测失败: %+v", diffs)
	}
}

func TestDetectExtraRow(t *testing.T) {
	src, dst := loadStores(10000)
	dst.Upsert(makeRow(99999))
	rep := NewInspector(src, dst).Inspect()
	diffs := rep.AllDiffs()
	if len(diffs) != 1 || diffs[0].ID != 99999 || diffs[0].Kind != ExtraInTarget {
		t.Fatalf("多行检测失败: %+v", diffs)
	}
}

// TestDetectTamperUnderContinuousFlow 验收场景：
// 源库持续产生变更并实时复制到目标库，期间悄悄篡改目标库某行单字段，
// 巡检器必须在变更持续流动的同时精确定位到该主键。
func TestDetectTamperUnderContinuousFlow(t *testing.T) {
	const totalRows = 100000
	src, dst := loadStores(totalRows)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 变更生成器：持续更新源库随机行
	changes := make(chan Row, 1<<16)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(1))
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < 200; i++ {
				id := int64(rng.Intn(totalRows))
				r, _ := src.Get(id)
				r.Balance += int64(rng.Intn(100))
				r.Version++
				r.UpdatedAt = time.Now().UnixNano()
				src.Upsert(r)
				changes <- r
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// 复制链路：把源库变更应用到目标库
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			case r := <-changes:
				dst.Upsert(r)
			}
		}
	}()
	defer func() { close(stop); wg.Wait() }()

	// 让变更流跑一会儿
	time.Sleep(300 * time.Millisecond)

	// 人为篡改目标库某行的单个字段（绕过复制链路）
	const victimID = 54321
	row, _ := dst.Get(victimID)
	row.Balance += 999999
	dst.Upsert(row)
	tamperedAt := time.Now()

	// 巡检：连续两个周期都出现的差异才判定为真实不一致
	// （单个周期内的差异可能只是复制延迟造成的瞬时状态）
	insp := NewInspector(src, dst)
	seen := map[int64]int{}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("5 秒内未定位到被篡改的行")
		default:
		}
		rep := insp.Inspect()
		current := map[int64]bool{}
		for _, d := range rep.AllDiffs() {
			current[d.ID] = true
			seen[d.ID]++
			if seen[d.ID] >= 2 && d.ID == victimID && d.Kind == Modified {
				latency := time.Since(tamperedAt)
				t.Logf("检测到篡改: pk=%d kind=%s 延迟=%s", d.ID, d.Kind, latency)
				if latency > 500*time.Millisecond {
					t.Fatalf("检测延迟过大: %s", latency)
				}
				return
			}
		}
		for id := range seen {
			if !current[id] {
				delete(seen, id) // 瞬时差异（复制延迟）已收敛
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
