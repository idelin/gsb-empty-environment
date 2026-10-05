package main

import (
	"math/rand"
	"reflect"
	"testing"
)

// 枚举验证：构造算法得到的截点必须等于所有有效截点的逐分量最大值
// （即“最大可恢复截点”），且自身有效。
func checkAgainstEnumeration(t *testing.T, parts []Partition, txns []Txn) {
	t.Helper()
	got := RecoverableCut(parts, txns)
	if !ValidCut(parts, txns, got) {
		t.Fatalf("构造截点无效: %v", got)
	}
	valid := EnumerateValidCuts(parts, txns)
	if len(valid) == 0 {
		t.Fatalf("枚举没有找到任何有效截点")
	}
	// 全零截点总是有效，作为逐分量最大值的起点。
	best := Cut{}
	for _, p := range parts {
		best[p.Name] = 0
	}
	for _, c := range valid {
		for k, v := range c {
			if v > best[k] {
				best[k] = v
			}
		}
		if Dominates(c, got) {
			t.Fatalf("存在更优有效截点 %v，构造结果 %v 非最大", c, got)
		}
	}
	if !reflect.DeepEqual(got, best) {
		t.Fatalf("构造截点 %v != 枚举最大截点 %v", got, best)
	}
}

func TestDemoScenario(t *testing.T) {
	parts, txns := demoScenario()
	checkAgainstEnumeration(t, parts, txns)

	cut := RecoverableCut(parts, txns)
	want := Cut{"A": 2, "B": 2, "C": 0}
	if !reflect.DeepEqual(cut, want) {
		t.Fatalf("截点 = %v, 期望 %v", cut, want)
	}

	included, ev := Analyze(parts, txns, cut)
	if !reflect.DeepEqual(included, []string{"t1"}) {
		t.Fatalf("包含事务 = %v, 期望 [t1]", included)
	}
	byID := map[string]Evidence{}
	for _, e := range ev {
		byID[e.TxnID] = e
	}
	if !reflect.DeepEqual(byID["t2"].Undecided, []string{"B"}) {
		t.Fatalf("t2 未决证据 = %v", byID["t2"].Undecided)
	}
	if !reflect.DeepEqual(byID["t3"].Missing, []string{"C"}) {
		t.Fatalf("t3 缺失参与者 = %v", byID["t3"].Missing)
	}
	if !reflect.DeepEqual(byID["t3"].LateCommits, []string{"B"}) {
		t.Fatalf("t3 迟到提交 = %v", byID["t3"].LateCommits)
	}
	if !reflect.DeepEqual(byID["t4"].LateCommits, []string{"A", "C"}) {
		t.Fatalf("t4 迟到提交 = %v", byID["t4"].LateCommits)
	}
}

func TestLateCommitBeyondFrontier(t *testing.T) {
	// t1 完整提交；t2 的提交标记在 A 上迟到（位于被回滚区域之后）。
	parts := []Partition{
		{Name: "A", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data},
			{Seq: 2, TxnID: "t1", Kind: Commit},
			{Seq: 3, TxnID: "t2", Kind: Data},
			{Seq: 4, TxnID: "t2", Kind: Commit},
		}},
		{Name: "B", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data},
			{Seq: 2, TxnID: "t1", Kind: Commit},
			{Seq: 3, TxnID: "t2", Kind: Data}, // B 上 t2 未决
		}},
	}
	txns := []Txn{
		{ID: "t1", Participants: []string{"A", "B"}},
		{ID: "t2", Participants: []string{"A", "B"}},
	}
	checkAgainstEnumeration(t, parts, txns)
	cut := RecoverableCut(parts, txns)
	if cut["A"] != 2 || cut["B"] != 2 {
		t.Fatalf("截点 = %v, 期望 A=2 B=2", cut)
	}
	_, ev := Analyze(parts, txns, cut)
	if len(ev) != 1 || !reflect.DeepEqual(ev[0].LateCommits, []string{"A"}) ||
		!reflect.DeepEqual(ev[0].Undecided, []string{"B"}) {
		t.Fatalf("证据 = %+v", ev)
	}
}

func TestAbortedTxnExcluded(t *testing.T) {
	parts := []Partition{
		{Name: "A", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data},
			{Seq: 2, TxnID: "t1", Kind: Abort},
		}},
		{Name: "B", Log: []Entry{
			{Seq: 1, TxnID: "t1", Kind: Data},
			{Seq: 2, TxnID: "t1", Kind: Abort},
		}},
	}
	txns := []Txn{{ID: "t1", Participants: []string{"A", "B"}}}
	checkAgainstEnumeration(t, parts, txns)
	cut := RecoverableCut(parts, txns)
	if cut["A"] != 0 || cut["B"] != 0 {
		t.Fatalf("中止事务的数据不得进入截点: %v", cut)
	}
}

// 随机小事务集合：生成小规模分区日志，枚举全部截点验证构造算法。
func TestRandomSmallSets(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	names := []string{"A", "B", "C"}
	for iter := 0; iter < 2000; iter++ {
		ntxn := 1 + rng.Intn(3)
		var txns []Txn
		logs := map[string][]Entry{}
		for i := 0; i < ntxn; i++ {
			id := string(rune('a' + i))
			// 随机参与者子集（1~3 个分区）。
			perm := rng.Perm(len(names))
			k := 1 + rng.Intn(len(names))
			var ps []string
			for _, j := range perm[:k] {
				ps = append(ps, names[j])
			}
			txns = append(txns, Txn{ID: id, Participants: ps})
			for _, pn := range ps {
				var es []Entry
				if rng.Intn(4) != 0 { // 75% 有数据
					es = append(es, Entry{TxnID: id, Kind: Data})
				}
				switch rng.Intn(4) { // 决定：commit / abort / 未决
				case 0:
					es = append(es, Entry{TxnID: id, Kind: Commit})
				case 1:
					es = append(es, Entry{TxnID: id, Kind: Abort})
				}
				logs[pn] = append(logs[pn], es...)
			}
		}
		var parts []Partition
		for _, nm := range names {
			es := logs[nm]
			rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
			for i := range es {
				es[i].Seq = i + 1
			}
			parts = append(parts, Partition{Name: nm, Log: es})
		}
		checkAgainstEnumeration(t, parts, txns)
	}
}
