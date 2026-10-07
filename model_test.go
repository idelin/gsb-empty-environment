package dedup

import (
	"fmt"
	"math/rand"
	"testing"
)

// fullModel 是全历史参考模型: 永久保留每条首次记录与所有来源进度,
// 从完整历史推导出任意时刻的正确行为(包括回收与重放判定),
// 用于逐步对照真实 Deduper。
type fullModel struct {
	retention int64
	first     map[string]modelRec // id -> 首次记录(永不删除)
	reclaimed map[string]bool     // id -> 记录是否已被水位回收
	progress  map[string]int64
	wm        int64 // 单调水位缓存
	hasWM     bool
}

type modelRec struct {
	payload string
	time    int64
}

func newFullModel(retention int64) *fullModel {
	return &fullModel{
		retention: retention,
		first:     make(map[string]modelRec),
		reclaimed: make(map[string]bool),
		progress:  make(map[string]int64),
	}
}

func (m *fullModel) watermark() (int64, bool) {
	return m.wm, m.hasWM
}

// advance 上报来源进度并按新水位回收, 返回 (新水位, 新回收的 id 数)。
func (m *fullModel) advance(source string, t int64) {
	if prog, ok := m.progress[source]; ok && t <= prog {
		return
	}
	m.progress[source] = t
	first := true
	var min int64
	for _, p := range m.progress {
		if first || p < min {
			min = p
			first = false
		}
	}
	if !m.hasWM || min > m.wm {
		m.wm = min
	}
	m.hasWM = true
	wm, ok := m.watermark()
	if !ok {
		return
	}
	for id, rec := range m.first {
		if !m.reclaimed[id] && rec.time+m.retention < wm {
			m.reclaimed[id] = true
		}
	}
}

// expectObserve 从全历史推导期望判定, 并推进模型状态。
//
// 关键性质: 已回收 id 的忠实重放(携带原事件时间)必须判 Stale,
// 绝不能因为记录已被回收而被当作新事件。
func (m *fullModel) expectObserve(e Event) (Verdict, string) {
	rec, seen := m.first[e.ID]
	if seen && !m.reclaimed[e.ID] {
		return Duplicate, rec.payload // 存活记录: 首次载荷获胜
	}
	if seen && m.reclaimed[e.ID] && e.Time == rec.time {
		return Stale, "" // 已回收记录的忠实重放
	}
	if prog, ok := m.progress[e.Source]; ok && e.Time <= prog {
		return Stale, ""
	}
	if wm, ok := m.watermark(); ok && e.Time+m.retention < wm {
		return Stale, ""
	}
	if seen {
		// 已回收 id 携带不同事件时间且未被进度判旧:
		// 属于生成器不应产生的歧义场景, 直接失败。
		panic(fmt.Sprintf("model: ambiguous replay of reclaimed id %q", e.ID))
	}
	m.first[e.ID] = modelRec{payload: e.Payload, time: e.Time}
	return New, e.Payload
}

func (m *fullModel) live() int {
	n := 0
	for id := range m.first {
		if !m.reclaimed[id] {
			n++
		}
	}
	return n
}

// TestFullHistoryModel 随机生成 观察/推进 混合操作序列,
// 每一步都用全历史模型核对真实去重器的判定、载荷、水位与存活数。
// 覆盖: 水位推进(多来源最小值/单调性)、不同来源进度隔离、
// 同 id 冲突(不同载荷/不同事件时间)、已回收 id 的重放。
func TestFullHistoryModel(t *testing.T) {
	const retention = 50
	sources := []string{"alpha", "beta", "gamma", "delta"}

	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		d := NewDeduper(retention)
		m := newFullModel(retention)

		// id 池刻意保持很小, 制造大量同 id 冲突与重放
		idPool := make([]string, 24)
		for i := range idPool {
			idPool[i] = fmt.Sprintf("id-%d", i)
		}

		var lastWM int64
		for step := 0; step < 3000; step++ {
			if rng.Intn(4) == 0 {
				// 推进某个来源的显式进度(单调)
				src := sources[rng.Intn(len(sources))]
				cur, _ := m.progress[src]
				ts := cur + int64(rng.Intn(40))
				d.Advance(src, ts)
				m.advance(src, ts)

				wm, ok := d.Watermark()
				mwm, mok := m.watermark()
				if ok != mok || (ok && wm != mwm) {
					t.Fatalf("seed %d step %d: watermark = %d,%v, model = %d,%v",
						seed, step, wm, ok, mwm, mok)
				}
				if ok && wm < lastWM {
					t.Fatalf("seed %d step %d: watermark regressed %d -> %d",
						seed, step, lastWM, wm)
				}
				if ok {
					lastWM = wm
				}
				continue
			}

			// 构造事件: 一半概率从 id 池取(制造冲突/重放),
			// 事件时间围绕当前最大进度波动(制造新旧混合)
			id := idPool[rng.Intn(len(idPool))]
			var maxProg int64
			for _, p := range m.progress {
				if p > maxProg {
					maxProg = p
				}
			}
			var et int64
			if rec, seen := m.first[id]; seen {
				if m.reclaimed[id] {
					et = rec.time // 忠实重放已回收记录
				} else if rng.Intn(3) == 0 {
					et = maxProg + int64(rng.Intn(120)) - 60 // 存活期同 id 时间冲突
				} else {
					et = rec.time
				}
			} else {
				et = maxProg + int64(rng.Intn(120)) - 60
			}
			if et < 0 {
				et = 0
			}
			e := Event{
				Source:  sources[rng.Intn(len(sources))],
				ID:      id,
				Time:    et,
				Payload: fmt.Sprintf("p-%d-%d", seed, step),
			}

			wantV, wantP := m.expectObserve(e)
			gotV, gotP := d.Observe(e)
			if gotV != wantV || gotP != wantP {
				t.Fatalf("seed %d step %d: observe %+v = %v,%q; model wants %v,%q",
					seed, step, e, gotV, gotP, wantV, wantP)
			}
			if d.Len() != m.live() {
				t.Fatalf("seed %d step %d: Len = %d, model live = %d",
					seed, step, d.Len(), m.live())
			}
		}

		// 收尾: 所有来源大幅推进后, 全部记录必须被回收
		for _, src := range sources {
			d.Advance(src, 1<<60)
			m.advance(src, 1<<60)
		}
		if d.Len() != 0 || m.live() != 0 {
			t.Fatalf("seed %d: after final advance Len = %d, model live = %d",
				seed, d.Len(), m.live())
		}
		if d.Reclaimed() != int64(len(m.first)) {
			t.Fatalf("seed %d: reclaimed = %d, model total = %d",
				seed, d.Reclaimed(), len(m.first))
		}
	}
}
