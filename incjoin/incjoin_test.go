package incjoin

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func ins(side Side, row Row, n int64) Op { return Op{side, Insert, row, n} }
func del(side Side, row Row, n int64) Op { return Op{side, Delete, row, n} }

// checkAgainstFull 校验增量结果与全量连接一致，且差量满足 result' = result + delta。
func checkAgainstFull(t *testing.T, e *Engine, prev map[OutRow]int64, delta map[OutRow]int64) {
	t.Helper()
	want := make(map[OutRow]int64)
	for row, n := range prev {
		want[row] = n
	}
	for row, d := range delta {
		want[row] += d
		if want[row] == 0 {
			delete(want, row)
		}
	}
	if got := e.Result(); !reflect.DeepEqual(got, want) {
		t.Fatalf("差量应用后结果不符:\n got=%v\nwant=%v", got, want)
	}
	if got, full := e.Result(), e.FullJoin(); !reflect.DeepEqual(got, full) {
		t.Fatalf("增量结果与全量连接不符:\n inc=%v\nfull=%v", got, full)
	}
}

// 重复键：两侧同键多行，结果重数为两侧重数之积。
func TestDuplicateKeys(t *testing.T) {
	e := New()
	if _, err := e.Apply([]Op{
		ins(Left, Row{"k", "a"}, 2),
		ins(Left, Row{"k", "b"}, 1),
		ins(Right, Row{"k", "x"}, 3),
		ins(Right, Row{"k", "y"}, 1),
	}); err != nil {
		t.Fatal(err)
	}
	prev := e.Result()
	delta, err := e.Apply([]Op{
		ins(Left, Row{"k", "a"}, 1),  // a: 2->3
		ins(Right, Row{"k", "x"}, 1), // x: 3->4
	})
	if err != nil {
		t.Fatal(err)
	}
	// 交叉增量: ΔL(a,+1)⋈R(x3,y1) + L(a2,b1)⋈ΔR(x,+1) + ΔL(a)⋈ΔR(x)
	wantDelta := map[OutRow]int64{
		{"k", "a", "x"}: 1*3 + 2*1 + 1*1, // 6
		{"k", "a", "y"}: 1 * 1,           // 1
		{"k", "b", "x"}: 1 * 1,           // 1
	}
	if !reflect.DeepEqual(delta, wantDelta) {
		t.Fatalf("差量不符:\n got=%v\nwant=%v", delta, wantDelta)
	}
	checkAgainstFull(t, e, prev, delta)
	full := e.FullJoin()
	if full[OutRow{"k", "a", "x"}] != 12 || full[OutRow{"k", "b", "y"}] != 1 {
		t.Fatalf("重数乘积错误: %v", full)
	}
}

// 删除至零：删除全部重数后，结果行应消失。
func TestDeleteToZero(t *testing.T) {
	e := New()
	must := func(ops []Op) map[OutRow]int64 {
		d, err := e.Apply(ops)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	must([]Op{ins(Left, Row{"k", "a"}, 2), ins(Right, Row{"k", "x"}, 2)})
	prev := e.Result()
	delta := must([]Op{del(Left, Row{"k", "a"}, 2)})
	checkAgainstFull(t, e, prev, delta)
	if len(e.Result()) != 0 {
		t.Fatalf("删除至零后结果应为空: %v", e.Result())
	}
	if _, ok := delta[OutRow{"k", "a", "x"}]; !ok || delta[OutRow{"k", "a", "x"}] != -4 {
		t.Fatalf("差量应为 -4: %v", delta)
	}
}

// 同批先删后加：同一批次内对同一行删除再插入，净重数为零，结果不变。
func TestDeleteThenAddSameBatch(t *testing.T) {
	e := New()
	must := func(ops []Op) map[OutRow]int64 {
		d, err := e.Apply(ops)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	must([]Op{ins(Left, Row{"k", "a"}, 3), ins(Right, Row{"k", "x"}, 2)})
	prev := e.Result()
	delta := must([]Op{
		del(Left, Row{"k", "a"}, 3), // 先删光
		ins(Left, Row{"k", "a"}, 3), // 同批加回
		del(Right, Row{"k", "x"}, 1),
		ins(Right, Row{"k", "x"}, 1),
	})
	if len(delta) != 0 {
		t.Fatalf("净变化为零，差量应为空: %v", delta)
	}
	checkAgainstFull(t, e, prev, delta)
	// 同批部分删加：净 +1
	prev = e.Result()
	delta = must([]Op{
		del(Left, Row{"k", "a"}, 1),
		ins(Left, Row{"k", "a"}, 2),
	})
	if delta[OutRow{"k", "a", "x"}] != 2 {
		t.Fatalf("净 +1 重数应产生 +2 差量: %v", delta)
	}
	checkAgainstFull(t, e, prev, delta)
}

// 负重数输入拒绝：删除超过现有重数时整批拒绝，状态不变。
func TestNegativeMultiplicityRejected(t *testing.T) {
	e := New()
	if _, err := e.Apply([]Op{ins(Left, Row{"k", "a"}, 2), ins(Right, Row{"k", "x"}, 1)}); err != nil {
		t.Fatal(err)
	}
	before := e.Result()
	// 同批中左表删除超量，右表插入合法：整批应被拒绝。
	if _, err := e.Apply([]Op{
		del(Left, Row{"k", "a"}, 3),
		ins(Right, Row{"k", "y"}, 5),
	}); err == nil {
		t.Fatal("应拒绝导致负重数的批次")
	}
	if got := e.Result(); !reflect.DeepEqual(got, before) {
		t.Fatalf("拒绝后状态不应改变: %v", got)
	}
	// 非法计数也应拒绝。
	if _, err := e.Apply([]Op{ins(Left, Row{"k", "b"}, 0)}); err == nil {
		t.Fatal("应拒绝 Count=0")
	}
	if _, err := e.Apply([]Op{ins(Left, Row{"k", "b"}, -2)}); err == nil {
		t.Fatal("应拒绝负 Count")
	}
}

// 交叉增量：同批同时向两表插入同键行，差量必须包含 ΔL⋈ΔR 项。
func TestCrossTermSameBatch(t *testing.T) {
	e := New()
	if _, err := e.Apply([]Op{ins(Left, Row{"k", "a"}, 2), ins(Right, Row{"k", "x"}, 3)}); err != nil {
		t.Fatal(err)
	}
	prev := e.Result()
	delta, err := e.Apply([]Op{
		ins(Left, Row{"k", "b"}, 2),
		ins(Right, Row{"k", "y"}, 5),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[OutRow]int64{
		{"k", "b", "x"}: 2 * 3, // ΔL⋈R
		{"k", "a", "y"}: 2 * 5, // L⋈ΔR
		{"k", "b", "y"}: 2 * 5, // ΔL⋈ΔR 交叉项
	}
	if !reflect.DeepEqual(delta, want) {
		t.Fatalf("交叉增量不符:\n got=%v\nwant=%v", delta, want)
	}
	checkAgainstFull(t, e, prev, delta)
}

// 随机批次对拍：每个批次后与全量连接对照。
func TestRandomizedAgainstFullJoin(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	e := New()
	keys := []string{"k1", "k2", "k3"}
	vals := []string{"a", "b", "c"}
	for batch := 0; batch < 500; batch++ {
		var ops []Op
		for i := 0; i < rng.Intn(8); i++ {
			side := Side(rng.Intn(2))
			row := Row{keys[rng.Intn(len(keys))], vals[rng.Intn(len(vals))]}
			if rng.Intn(2) == 0 {
				ops = append(ops, ins(side, row, int64(rng.Intn(3)+1)))
			} else {
				ops = append(ops, del(side, row, int64(rng.Intn(3)+1)))
			}
		}
		prev := e.Result()
		delta, err := e.Apply(ops)
		if err != nil {
			continue // 负重数批次被拒绝，状态不变
		}
		checkAgainstFull(t, e, prev, delta)
		for row, n := range e.Result() {
			if n <= 0 {
				t.Fatalf("批次 %d: 结果含非正重数 %v=%d", batch, row, n)
			}
		}
	}
}

func Example() {
	e := New()
	delta, _ := e.Apply([]Op{
		ins(Left, Row{"k", "a"}, 2),
		ins(Right, Row{"k", "x"}, 3),
	})
	fmt.Println(delta[OutRow{"k", "a", "x"}])
	// Output: 6
}
