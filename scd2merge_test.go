package scd2merge

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"
)

func at(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func upsert(id, key string, sec int64, value string) Event[string] {
	return Event[string]{ID: id, Key: key, At: at(sec), Op: OpUpsert, Value: value}
}

func del(id, key string, sec int64) Event[string] {
	return Event[string]{ID: id, Key: key, At: at(sec), Op: OpDelete}
}

func iv(from, to int64, value string) Interval[string] {
	var t time.Time
	if to >= 0 {
		t = at(to)
	}
	return Interval[string]{From: at(from), To: t, Value: value}
}

func checkIntervals(t *testing.T, s *Store[string], key string, want []Interval[string]) {
	t.Helper()
	got := s.Intervals(key)
	if !slices.Equal(got, want) {
		t.Fatalf("key %q intervals = %v, want %v", key, got, want)
	}
}

func TestLateArrivalSplitsInterval(t *testing.T) {
	s := New[string]()
	s.Apply(upsert("e1", "k", 10, "A"))
	s.Apply(upsert("e3", "k", 30, "C"))
	// 迟到的变更插入到中间，切分已有区间。
	s.Apply(upsert("e2", "k", 20, "B"))
	checkIntervals(t, s, "k", []Interval[string]{
		iv(10, 20, "A"), iv(20, 30, "B"), iv(30, -1, "C"),
	})
}

func TestAdjacentSameContentMerged(t *testing.T) {
	s := New[string]()
	s.Apply(upsert("e1", "k", 10, "A"))
	s.Apply(upsert("e2", "k", 20, "B"))
	s.Apply(upsert("e3", "k", 30, "A"))
	// 中间段被迟到的 A 覆盖后，三段相同内容合并为一段。
	s.Apply(upsert("e4", "k", 20, "A"))
	checkIntervals(t, s, "k", []Interval[string]{iv(10, -1, "A")})
}

func TestDuplicateEventIdempotent(t *testing.T) {
	s := New[string]()
	ev := upsert("e1", "k", 10, "A")
	if !s.Apply(ev) {
		t.Fatal("first apply should be accepted")
	}
	if s.Apply(ev) {
		t.Fatal("duplicate apply should be rejected")
	}
	// 内容相同的重复变更即使 ID 不同也不产生新区间。
	s.Apply(upsert("e2", "k", 10, "A"))
	s.Apply(upsert("e3", "k", 15, "A"))
	checkIntervals(t, s, "k", []Interval[string]{iv(10, -1, "A")})
}

func TestDeleteClosesInterval(t *testing.T) {
	s := New[string]()
	s.Apply(upsert("e1", "k", 10, "A"))
	s.Apply(del("e2", "k", 20))
	checkIntervals(t, s, "k", []Interval[string]{iv(10, 20, "A")})
	if _, ok := s.ValueAt("k", at(20)); ok {
		t.Fatal("value should be gone at delete time")
	}
	if v, ok := s.ValueAt("k", at(15)); !ok || v != "A" {
		t.Fatalf("ValueAt(15) = %v, %v", v, ok)
	}
	// 删除后空洞中的迟到变更只到下一区间为止。
	s.Apply(upsert("e3", "k", 30, "B"))
	s.Apply(upsert("e4", "k", 25, "C"))
	checkIntervals(t, s, "k", []Interval[string]{
		iv(10, 20, "A"), iv(25, 30, "C"), iv(30, -1, "B"),
	})
	// 重复删除幂等。
	s.Apply(del("e2", "k", 20))
	checkIntervals(t, s, "k", []Interval[string]{
		iv(10, 20, "A"), iv(25, 30, "C"), iv(30, -1, "B"),
	})
}

func TestDeleteAtIntervalStart(t *testing.T) {
	s := New[string]()
	s.Apply(upsert("e1", "k", 10, "A"))
	s.Apply(upsert("e2", "k", 20, "B"))
	// 删除时刻恰为区间起点：整段作废。
	s.Apply(del("e3", "k", 20))
	checkIntervals(t, s, "k", []Interval[string]{iv(10, 20, "A")})
}

func TestSameTimestampDeleteWins(t *testing.T) {
	events := []Event[string]{
		upsert("u1", "k", 10, "A"),
		del("d1", "k", 10),
	}
	// 无论到达顺序如何，同刻删除都生效。
	s1 := New[string]()
	s1.ApplyAll(events)
	s2 := New[string]()
	s2.ApplyAll([]Event[string]{events[1], events[0]})
	for i, s := range []*Store[string]{s1, s2} {
		if got := s.Intervals("k"); len(got) != 0 {
			t.Fatalf("order %d: intervals = %v, want empty", i, got)
		}
	}
}

func TestSameTimestampUpsertLargerIDWins(t *testing.T) {
	s := New[string]()
	s.Apply(upsert("e9", "k", 10, "B"))
	// 同刻 ID 更小的迟到事件被忽略。
	s.Apply(upsert("e1", "k", 10, "A"))
	checkIntervals(t, s, "k", []Interval[string]{iv(10, -1, "B")})
}

// TestShuffledMatchesRebuild 随机生成事件流（含迟到、重复、删除、
// 同刻冲突），打乱到达顺序后与按事件时间全量重建的结果对比，
// 并校验任一时刻每个键最多一个有效版本。
func TestShuffledMatchesRebuild(t *testing.T) {
	const (
		keys        = 5
		eventsPerK  = 40
		seeds       = 2000
		maxEventSec = 20 // 小时间范围制造大量同刻冲突
	)
	values := []string{"A", "B", "C"}

	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var events []Event[string]
		id := 0
		nextID := func() string { id++; return fmt.Sprintf("e%04d", id) }
		for k := 0; k < keys; k++ {
			key := fmt.Sprintf("key-%d", k)
			for n := 0; n < eventsPerK; n++ {
				sec := int64(rng.Intn(maxEventSec))
				if rng.Intn(4) == 0 {
					events = append(events, del(nextID(), key, sec))
				} else {
					events = append(events, upsert(nextID(), key, sec, values[rng.Intn(len(values))]))
				}
			}
		}
		// 复制一部分事件制造重复到达。
		for _, ev := range events {
			if rng.Intn(4) == 0 {
				events = append(events, ev)
			}
		}

		want := Rebuild(events)

		shuffled := slices.Clone(events)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := New[string]()
		got.ApplyAll(shuffled)

		for k := 0; k < keys; k++ {
			key := fmt.Sprintf("key-%d", k)
			gotIv, wantIv := got.Intervals(key), want.Intervals(key)
			if !slices.Equal(gotIv, wantIv) {
				t.Fatalf("seed %d key %q:\n got %v\nwant %v", seed, key, gotIv, wantIv)
			}
			assertInvariant(t, seed, key, gotIv)
		}
	}
}

// assertInvariant 校验区间有序、互不重叠、非空，即任一时刻最多一个有效版本。
func assertInvariant[V comparable](t *testing.T, seed int64, key string, ivs []Interval[V]) {
	t.Helper()
	for i, cur := range ivs {
		if !cur.To.IsZero() && !cur.From.Before(cur.To) {
			t.Fatalf("seed %d key %q: empty interval %v", seed, key, cur)
		}
		if i == 0 {
			continue
		}
		prev := ivs[i-1]
		if prev.To.IsZero() || cur.From.Before(prev.To) {
			t.Fatalf("seed %d key %q: overlapping intervals %v and %v", seed, key, prev, cur)
		}
		if prev.To.Equal(cur.From) && prev.Value == cur.Value {
			t.Fatalf("seed %d key %q: adjacent equal intervals not mergeded: %v and %v", seed, key, prev, cur)
		}
	}
}
