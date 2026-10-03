package occ

import (
	"math/rand"
	"strconv"
	"testing"

	"occexample/lockkv"
)

const (
	benchKeys = 1024
	txnKeys   = 4 // 每个事务读取的键数
)

func seedOCC(e *Engine) {
	tx := e.Begin()
	for i := 0; i < benchKeys; i++ {
		tx.Set(strconv.Itoa(i), i)
	}
	if err := tx.Commit(); err != nil {
		panic(err)
	}
}

func seedLock(s *lockkv.Store) {
	tx := s.Begin(true)
	for i := 0; i < benchKeys; i++ {
		tx.Set(strconv.Itoa(i), i)
	}
	tx.Commit()
}

// 读多写少：每个事务读 4 个键，5% 的事务额外写 1 个键。
func BenchmarkOCC_ReadHeavy(b *testing.B) {
	e := New()
	seedOCC(e)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(1))
		for pb.Next() {
			write := r.Intn(100) < 5
			tx := e.Begin()
			for i := 0; i < txnKeys; i++ {
				tx.Get(strconv.Itoa(r.Intn(benchKeys)))
			}
			if write {
				tx.Set(strconv.Itoa(r.Intn(benchKeys)), 1)
			}
			tx.Commit()
		}
	})
}

func BenchmarkLock_ReadHeavy(b *testing.B) {
	s := lockkv.New()
	seedLock(s)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(1))
		for pb.Next() {
			write := r.Intn(100) < 5
			tx := s.Begin(write)
			for i := 0; i < txnKeys; i++ {
				tx.Get(strconv.Itoa(r.Intn(benchKeys)))
			}
			if write {
				tx.Set(strconv.Itoa(r.Intn(benchKeys)), 1)
			}
			tx.Commit()
		}
	})
}

// 纯读负载：OCC 读路径完全无锁，扩展性优势最明显。
func BenchmarkOCC_PureRead(b *testing.B) {
	e := New()
	seedOCC(e)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(1))
		for pb.Next() {
			tx := e.Begin()
			for i := 0; i < txnKeys; i++ {
				tx.Get(strconv.Itoa(r.Intn(benchKeys)))
			}
			tx.Commit()
		}
	})
}

func BenchmarkLock_PureRead(b *testing.B) {
	s := lockkv.New()
	seedLock(s)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(1))
		for pb.Next() {
			tx := s.Begin(false)
			for i := 0; i < txnKeys; i++ {
				tx.Get(strconv.Itoa(r.Intn(benchKeys)))
			}
			tx.Commit()
		}
	})
}

// 热点键写冲突：OCC 冲突率统计演示。
func BenchmarkOCC_HotKeyWrite(b *testing.B) {
	e := New()
	seed := e.Begin()
	seed.Set("hot", 0)
	seed.Commit()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tx := e.Begin()
			v, _ := tx.Get("hot")
			tx.Set("hot", v.(int)+1)
			tx.Commit() // 大量冲突在此产生
		}
	})
	b.StopTimer()
	commits, conflicts := e.Stats()
	b.Logf("commits=%d conflicts=%d conflict-rate=%.2f%%",
		commits, conflicts, float64(conflicts)/float64(commits+conflicts)*100)
}
