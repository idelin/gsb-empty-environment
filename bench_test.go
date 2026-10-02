package occ

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

const benchKeys = 64

var benchKeyNames [benchKeys]string
var hotKeyNames [8]string

func init() {
	for i := 0; i < benchKeys; i++ {
		benchKeyNames[i] = fmt.Sprintf("k%d", i)
	}
	for i := 0; i < 8; i++ {
		hotKeyNames[i] = fmt.Sprintf("h%d", i)
	}
}

// read-heavy workload: 95% read-only txns, 5% read-modify-write txns,
// keys drawn from a small hot set (zipf-ish via modulo of rand).

func BenchmarkOCC_ReadHeavy(b *testing.B) {
	s := NewStore()
	for i := 0; i < benchKeys; i++ {
		s.Put(fmt.Sprintf("k%d", i), "0")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(1))
		for pb.Next() {
			key := benchKeyNames[rng.Intn(benchKeys)]
			if rng.Intn(100) < 5 {
				s.Transact(func(txn *Txn) {
					n, _ := strconv.Atoi(txn.Read(key))
					txn.Write(key, strconv.Itoa(n+1))
				})
			} else {
				txn := s.Begin()
				txn.Read(key)
				txn.Read(benchKeyNames[rng.Intn(benchKeys)])
				txn.Commit()
			}
		}
	})
	b.StopTimer()
	st := s.Stats()
	b.Logf("commits=%d aborts=%d conflictRate=%.2f%%", st.Commits, st.Aborts, 100*st.ConflictRate())
}

func BenchmarkLock_ReadHeavy(b *testing.B) {
	l := NewLockStore()
	for i := 0; i < benchKeys; i++ {
		l.Put(fmt.Sprintf("k%d", i), "0")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(1))
		for pb.Next() {
			key := benchKeyNames[rng.Intn(benchKeys)]
			if rng.Intn(100) < 5 {
				l.Transact(func(get func(string) string, set func(string, string)) {
					n, _ := strconv.Atoi(get(key))
					set(key, strconv.Itoa(n+1))
				})
			} else {
				l.Get(key)
				l.Get(benchKeyNames[rng.Intn(benchKeys)])
			}
		}
	})
}

// write-heavy hotspot: 50% writes on only 8 hot keys.

func BenchmarkOCC_HotKeys(b *testing.B) {
	s := NewStore()
	for i := 0; i < 8; i++ {
		s.Put(fmt.Sprintf("h%d", i), "0")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(2))
		for pb.Next() {
			key := hotKeyNames[rng.Intn(8)]
			if rng.Intn(100) < 50 {
				s.Transact(func(txn *Txn) {
					n, _ := strconv.Atoi(txn.Read(key))
					txn.Write(key, strconv.Itoa(n+1))
				})
			} else {
				txn := s.Begin()
				txn.Read(key)
				txn.Commit()
			}
		}
	})
	b.StopTimer()
	st := s.Stats()
	b.Logf("commits=%d aborts=%d conflictRate=%.2f%%", st.Commits, st.Aborts, 100*st.ConflictRate())
}

func BenchmarkLock_HotKeys(b *testing.B) {
	l := NewLockStore()
	for i := 0; i < 8; i++ {
		l.Put(fmt.Sprintf("h%d", i), "0")
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		rng := rand.New(rand.NewSource(2))
		for pb.Next() {
			key := hotKeyNames[rng.Intn(8)]
			if rng.Intn(100) < 50 {
				l.Transact(func(get func(string) string, set func(string, string)) {
					n, _ := strconv.Atoi(get(key))
					set(key, strconv.Itoa(n+1))
				})
			} else {
				l.Get(key)
			}
		}
	})
}
