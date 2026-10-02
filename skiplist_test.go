package skiplist

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

const totalKeys = 10000

func valueOf(key int64) []byte {
	return []byte(fmt.Sprintf("value-%d", key))
}

// shuffledChunks 返回 keys 的随机排列，并按 n 份切分，供多 goroutine 并发插入。
func shuffledChunks(r *rand.Rand, keys []int64, n int) [][]int64 {
	perm := r.Perm(len(keys))
	chunks := make([][]int64, n)
	for i, p := range perm {
		chunks[i%n] = append(chunks[i%n], keys[p])
	}
	return chunks
}

func concurrentSet(sl *SkipList, chunks [][]int64) {
	var wg sync.WaitGroup
	for _, chunk := range chunks {
		wg.Add(1)
		go func(keys []int64) {
			defer wg.Done()
			for _, k := range keys {
				sl.Set(k, valueOf(k))
			}
		}(chunk)
	}
	wg.Wait()
}

// 验收边界 1：连续随机并发插入 1 万个整数键值对，
// 之后全量扫描必须严格单调递增、数量完整、点查全部命中。
func TestConcurrentInsertTenThousand(t *testing.T) {
	sl := New()
	keys := make([]int64, totalKeys)
	for i := range keys {
		keys[i] = int64(i)
	}
	r := rand.New(rand.NewSource(42))
	concurrentSet(sl, shuffledChunks(r, keys, 8))

	it := sl.NewIterator(0, 1<<62)
	count := 0
	prev := int64(-1)
	for it.Next() {
		if it.Key() <= prev {
			t.Fatalf("迭代输出非严格单调递增: prev=%d cur=%d", prev, it.Key())
		}
		prev = it.Key()
		if got, want := string(it.Value()), string(valueOf(it.Key())); got != want {
			t.Fatalf("key=%d 值不匹配: got=%q want=%q", it.Key(), got, want)
		}
		count++
	}
	if count != totalKeys {
		t.Fatalf("扫描数量 = %d, 期望 %d", count, totalKeys)
	}

	for _, k := range keys {
		v, ok := sl.Get(k)
		if !ok || string(v) != string(valueOf(k)) {
			t.Fatalf("Get(%d) = %q, %v", k, v, ok)
		}
	}
}

// 验收边界 2：任意区间迭代器输出严格单调递增，且与期望区间完全一致。
func TestRangeIteratorMonotonic(t *testing.T) {
	sl := New()
	keys := make([]int64, totalKeys)
	for i := range keys {
		keys[i] = int64(i)
	}
	r := rand.New(rand.NewSource(7))
	concurrentSet(sl, shuffledChunks(r, keys, 4))

	ranges := [][2]int64{
		{0, totalKeys},    // 全量
		{100, 200},        // 小区间
		{0, 1},            // 单元素
		{5000, 5000},      // 空区间
		{9990, totalKeys}, // 尾部
		{-100, 50},        // 起点小于最小键
		{3333, 7777},      // 中间区间
	}
	for _, rg := range ranges {
		start, end := rg[0], rg[1]
		var got []int64
		prev := int64(-1 << 62)
		it := sl.NewIterator(start, end)
		for it.Next() {
			k := it.Key()
			if k <= prev {
				t.Fatalf("区间 [%d,%d) 输出非严格单调递增: prev=%d cur=%d", start, end, prev, k)
			}
			if k < start || k >= end {
				t.Fatalf("key=%d 越出区间 [%d,%d)", k, start, end)
			}
			prev = k
			got = append(got, k)
		}
		lo := start
		if lo < 0 {
			lo = 0
		}
		var want []int64
		for k := lo; k < end && k < totalKeys; k++ {
			want = append(want, k)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("区间 [%d,%d) 结果不匹配: got=%d 个, want=%d 个", start, end, len(got), len(want))
		}
	}
}

// 验收边界 3：迭代遍历过程中持续并发写入新数据，
// 迭代器输出结果集必须完全稳定隔离。
func TestIteratorSnapshotIsolation(t *testing.T) {
	sl := New()
	base := make([]int64, totalKeys)
	for i := range base {
		base[i] = int64(i) // 初始数据: [0, 10000)
	}
	r := rand.New(rand.NewSource(99))
	concurrentSet(sl, shuffledChunks(r, base, 4))

	// 在写入开始前创建快照迭代器，区间覆盖未来写入的键空间。
	it := sl.NewIterator(0, 2*totalKeys)

	// 遍历期间持续插入新键 [10000, 20000)，并覆写部分旧键。
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := int64(totalKeys + id); k < 2*totalKeys; k += 4 {
				sl.Set(k, valueOf(k))
			}
			for k := int64(id); k < 1000; k += 4 {
				sl.Set(k, []byte("overwritten"))
			}
		}(w)
	}

	// 与并发写入同时遍历快照。
	var got []int64
	for it.Next() {
		got = append(got, it.Key())
		if string(it.Value()) != string(valueOf(it.Key())) {
			t.Fatalf("key=%d 快照值被并发覆写污染: %q", it.Key(), it.Value())
		}
	}
	wg.Wait()

	if !reflect.DeepEqual(got, base) {
		t.Fatalf("快照结果集不稳定: got=%d 个键, 期望 %d 个键", len(got), len(base))
	}

	// 写入确实生效：新建迭代器应看到全部 2 万个键。
	it2 := sl.NewIterator(0, 2*totalKeys)
	count := 0
	for it2.Next() {
		count++
	}
	if count != 2*totalKeys {
		t.Fatalf("写入后扫描数量 = %d, 期望 %d", count, 2*totalKeys)
	}
}

// Example 演示基本用法：插入、点查、区间快照扫描。
func Example() {
	sl := New()
	for i := int64(0); i < 10; i++ {
		sl.Set(i, []byte(fmt.Sprintf("v%d", i)))
	}
	if v, ok := sl.Get(5); ok {
		fmt.Println("Get(5) =", string(v))
	}
	it := sl.NewIterator(3, 7)
	for it.Next() {
		fmt.Printf("%d -> %s\n", it.Key(), it.Value())
	}
	// Output:
	// Get(5) = v5
	// 3 -> v3
	// 4 -> v4
	// 5 -> v5
	// 6 -> v6
}
