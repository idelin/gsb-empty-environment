// demo 演示并发插入 1 万个键值对、区间扫描与迭代器快照隔离。
package main

import (
	"fmt"
	"math/rand"
	"sync"

	skiplist "example.com/skiplist"
)

func main() {
	sl := skiplist.New()

	// 1. 8 个 goroutine 并发随机插入 1 万个整数键值对。
	const total = 10000
	perm := rand.Perm(total)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := id; i < total; i += 8 {
				k := int64(perm[i])
				sl.Set(k, []byte(fmt.Sprintf("value-%d", k)))
			}
		}(w)
	}
	wg.Wait()
	fmt.Println("并发插入完成: 10000 个键值对")

	// 2. 区间扫描，输出严格单调递增。
	it := sl.NewIterator(4995, 5005)
	fmt.Print("区间 [4995, 5005) 扫描: ")
	for it.Next() {
		fmt.Printf("%d ", it.Key())
	}
	fmt.Println()

	// 3. 创建快照迭代器后持续写入，结果集完全稳定。
	snap := sl.NewIterator(0, 20000)
	go func() {
		for k := int64(total); k < 20000; k++ {
			sl.Set(k, []byte("new"))
		}
	}()
	count := 0
	for snap.Next() {
		count++
	}
	fmt.Printf("快照迭代器输出 %d 个键（写入期间保持稳定）\n", count)
}
