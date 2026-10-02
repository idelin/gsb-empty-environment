// demo 演示 MemTable -> Immutable MemTable -> L0 SSTable 的分层转换:
// 高频写入触发 4MB 阈值轮转, 后台单线程顺序刷盘, 最后全量校验数据。
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lsm "example.com/lsm"
)

const total = 120000 // 约 15MB, 触发 3 次 4MB 轮转

func value(i int) []byte {
	return []byte(fmt.Sprintf("payload-%08d-%s", i, strings.Repeat("x", 90)))
}

func main() {
	dir := "demo-data"
	os.RemoveAll(dir)

	eng, err := lsm.Open(dir, nil) // 默认 4MB 阈值
	if err != nil {
		log.Fatal(err)
	}

	stop := make(chan struct{})
	go func() { // 监控协程: 观察轮转与刷盘事件
		last := lsm.Stats{}
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				s := eng.Stats()
				if s != last {
					fmt.Printf("[monitor] rotations=%d flushes=%d queue=%d l0files=%d\n",
						s.Rotations, s.Flushes, s.QueueLength, s.L0Files)
					last = s
				}
			}
		}
	}()

	fmt.Printf("writing %d records (4MB memtable threshold)...\n", total)
	start := time.Now()
	var maxLat time.Duration
	for i := 0; i < total; i++ {
		t0 := time.Now()
		if err := eng.Put([]byte(fmt.Sprintf("user%08d", i)), value(i)); err != nil {
			log.Fatal(err)
		}
		if d := time.Since(t0); d > maxLat {
			maxLat = d
		}
	}
	writeDur := time.Since(start)
	fmt.Printf("write phase: %d records in %v (%.0f ops/s), max put latency %v\n",
		total, writeDur, float64(total)/writeDur.Seconds(), maxLat)

	// 等待刷盘追平(队列清空)。
	for {
		s := eng.Stats()
		if s.QueueLength == 0 && s.Flushes == s.Rotations {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	s := eng.Stats()
	fmt.Printf("final state: rotations=%d flushes=%d l0files=%d\n", s.Rotations, s.Flushes, s.L0Files)

	// 全量校验: 所有 key 可读且值正确。
	vstart := time.Now()
	for i := 0; i < total; i++ {
		got, ok, err := eng.Get(fmt.Sprintf("user%08d", i))
		if err != nil || !ok || string(got) != string(value(i)) {
			log.Fatalf("verify failed at %d: ok=%v err=%v", i, ok, err)
		}
	}
	fmt.Printf("verified %d records in %v (memtable + L0 sstables)\n", total, time.Since(vstart))

	if err := eng.Close(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("engine closed: active memtable flushed, all WAL segments rotated out")

	// 崩溃后重开视角: 仅从磁盘(SSTable)恢复并抽查。
	eng2, err := lsm.Open(dir, nil)
	if err != nil {
		log.Fatal(err)
	}
	for i := 0; i < total; i += 997 {
		got, ok, err := eng2.Get(fmt.Sprintf("user%08d", i))
		if err != nil || !ok || string(got) != string(value(i)) {
			log.Fatalf("post-reopen verify failed at %d: ok=%v err=%v", i, ok, err)
		}
	}
	fmt.Printf("post-reopen sample check passed (%d keys from L0 sstables)\n", total/997+1)
	eng2.Close()

	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		fi, _ := e.Info()
		names = append(names, fmt.Sprintf("%s(%.1fMB)", e.Name(), float64(fi.Size())/(1<<20)))
	}
	sort.Strings(names)
	abs, _ := filepath.Abs(dir)
	fmt.Printf("data dir %s: %s\n", abs, strings.Join(names, " "))
}
