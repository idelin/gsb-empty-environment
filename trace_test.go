package trace

import (
	"math/big"
	"strings"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("非法有理数 %q", s)
	}
	return r
}

func mustLoad(t *testing.T, l *Lab, name string, vol int64, masses map[string]string) {
	t.Helper()
	if err := l.AddContainer(name); err != nil {
		t.Fatal(err)
	}
	if err := l.Load(name, vol, masses); err != nil {
		t.Fatal(err)
	}
}

func checkSolute(t *testing.T, l *Lab, container, solute, mass string, shares map[int]string) {
	t.Helper()
	snap, ok := l.Snapshot(container)
	if !ok {
		t.Fatalf("容器 %q 不存在", container)
	}
	st, ok := snap.Solutes[solute]
	if !ok {
		t.Fatalf("容器 %q 无溶质 %q", container, solute)
	}
	if st.Mass.Cmp(rat(t, mass)) != 0 {
		t.Errorf("容器 %q 溶质 %q 质量 = %s, 期望 %s", container, solute, st.Mass.RatString(), mass)
	}
	if len(st.Shares) != len(shares) {
		t.Errorf("容器 %q 溶质 %q 份额数 = %d, 期望 %d (%v)", container, solute, len(st.Shares), len(shares), st.Shares)
	}
	for b, want := range shares {
		got, ok := st.Shares[b]
		if !ok {
			t.Errorf("容器 %q 溶质 %q 缺少批次 %d 份额", container, solute, b)
			continue
		}
		if got.Cmp(rat(t, want)) != 0 {
			t.Errorf("容器 %q 溶质 %q 批次 %d 份额 = %s, 期望 %s", container, solute, b, got.RatString(), want)
		}
	}
}

func checkVolume(t *testing.T, l *Lab, container, vol string) {
	t.Helper()
	snap, ok := l.Snapshot(container)
	if !ok {
		t.Fatalf("容器 %q 不存在", container)
	}
	if snap.Volume.Cmp(rat(t, vol)) != 0 {
		t.Errorf("容器 %q 体积 = %s, 期望 %s", container, snap.Volume.RatString(), vol)
	}
}

// 按源浓度比例精确取质量（有理数无舍入）。
func TestProportionalTransfer(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	id, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("批次 ID = %d, 期望 1", id)
	}
	checkVolume(t, l, "A", "70")
	checkVolume(t, l, "B", "30")
	checkSolute(t, l, "A", "salt", "7/30", map[int]string{0: "7/30"})
	checkSolute(t, l, "B", "salt", "1/10", map[int]string{1: "1/10"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 单次转移超量：拒绝且状态不变。
func TestOverdrawRejected(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: 101}}); err == nil {
		t.Fatal("超量转移应被拒绝")
	}
	checkVolume(t, l, "A", "100")
	checkVolume(t, l, "B", "0")
	checkSolute(t, l, "A", "salt", "1/3", map[int]string{0: "1/3"})
	if len(l.Batches()) != 0 {
		t.Error("被拒绝的批次不应记入日志")
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 批次按同一旧状态计算：单笔均合法但总取量超量，整批拒绝、无部分生效。
func TestBatchAtomicRejection(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	for _, n := range []string{"B", "C"} {
		if err := l.AddContainer(n); err != nil {
			t.Fatal(err)
		}
	}
	_, err := l.TransferBatch([]Transfer{
		{From: "A", To: "B", Volume: 60},
		{From: "A", To: "C", Volume: 50},
	})
	if err == nil || !strings.Contains(err.Error(), "批次拒绝") {
		t.Fatalf("总取量 110 > 100 应整批拒绝, got %v", err)
	}
	checkVolume(t, l, "A", "100")
	checkVolume(t, l, "B", "0")
	checkVolume(t, l, "C", "0")
	checkSolute(t, l, "A", "salt", "1/3", map[int]string{0: "1/3"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 批次内多笔从同一源取液，均按旧状态比例计算；恰好取空合法。
func TestBatchSameOldState(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	for _, n := range []string{"B", "C"} {
		if err := l.AddContainer(n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.TransferBatch([]Transfer{
		{From: "A", To: "B", Volume: 50},
		{From: "A", To: "C", Volume: 50},
	}); err != nil {
		t.Fatal(err)
	}
	// 两笔各按旧浓度 1/3/100 取 50：各得 1/6，A 恰好取空。
	checkVolume(t, l, "A", "0")
	checkSolute(t, l, "A", "salt", "0", map[int]string{})
	checkSolute(t, l, "B", "salt", "1/6", map[int]string{1: "1/6"})
	checkSolute(t, l, "C", "salt", "1/6", map[int]string{1: "1/6"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 空容器：正向取液拒绝，零体积转移为空操作，向空容器注液合法。
func TestEmptyContainer(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("E"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{{From: "E", To: "A", Volume: 10}}); err == nil {
		t.Fatal("从空容器取液应被拒绝")
	}
	// 零体积转移是合法空操作。
	if _, err := l.TransferBatch([]Transfer{{From: "E", To: "A", Volume: 0}}); err != nil {
		t.Fatalf("零体积转移应合法: %v", err)
	}
	checkVolume(t, l, "E", "0")
	checkVolume(t, l, "A", "100")
	// 向空容器注液。
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "E", Volume: 40}}); err != nil {
		t.Fatal(err)
	}
	checkVolume(t, l, "E", "40")
	checkSolute(t, l, "E", "salt", "2/15", map[int]string{2: "2/15"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 回流：A->B 后 B->A，份额正确合并与再分配。
func TestBackflow(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: 50}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{{From: "B", To: "A", Volume: 25}}); err != nil {
		t.Fatal(err)
	}
	checkVolume(t, l, "A", "75")
	checkVolume(t, l, "B", "25")
	// A: 原有 1/6（批次0）+ 回流 1/12（批次2）= 1/4
	checkSolute(t, l, "A", "salt", "1/4", map[int]string{0: "1/6", 2: "1/12"})
	checkSolute(t, l, "B", "salt", "1/12", map[int]string{1: "1/12"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 多源合并：同批 A->C、B->C，目的容器质量与份额正确累加。
func TestMultiSourceMerge(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	mustLoad(t, l, "B", 100, map[string]string{"salt": "1/2", "sugar": "7/8"})
	if err := l.AddContainer("C"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{
		{From: "A", To: "C", Volume: 25},
		{From: "B", To: "C", Volume: 25},
	}); err != nil {
		t.Fatal(err)
	}
	checkVolume(t, l, "C", "50")
	// salt: 1/3*1/4 + 1/2*1/4 = 1/12 + 1/8 = 5/24
	checkSolute(t, l, "C", "salt", "5/24", map[int]string{1: "5/24"})
	checkSolute(t, l, "C", "sugar", "7/32", map[int]string{1: "7/32"})
	checkSolute(t, l, "A", "salt", "1/4", map[int]string{0: "1/4"})
	checkSolute(t, l, "B", "salt", "3/8", map[int]string{0: "3/8"})
	checkSolute(t, l, "B", "sugar", "21/32", map[int]string{0: "21/32"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 加水稀释后按新浓度转移；混匀为合法操作。
func TestDilutionAndMix(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	if err := l.AddWater("A", 100); err != nil {
		t.Fatal(err)
	}
	if err := l.Mix("A"); err != nil {
		t.Fatal(err)
	}
	checkVolume(t, l, "A", "200")
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: 100}}); err != nil {
		t.Fatal(err)
	}
	// 稀释后浓度减半：100/200 取走一半质量。
	checkSolute(t, l, "A", "salt", "1/6", map[int]string{0: "1/6"})
	checkSolute(t, l, "B", "salt", "1/6", map[int]string{1: "1/6"})
	if err := l.Mix("missing"); err == nil {
		t.Fatal("混匀不存在的容器应报错")
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 自转移为空操作，不影响状态。
func TestSelfTransferNoop(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "A", Volume: 50}}); err != nil {
		t.Fatal(err)
	}
	checkVolume(t, l, "A", "100")
	checkSolute(t, l, "A", "salt", "1/3", map[int]string{0: "1/3"})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// 非法输入：负体积、负质量、不存在容器、重复注册、重复装载。
func TestInvalidInputs(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("A"); err == nil {
		t.Error("重复注册应报错")
	}
	if err := l.Load("A", 10, nil); err == nil {
		t.Error("非空容器重复装载应报错")
	}
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	if err := l.Load("B", 10, map[string]string{"x": "-1"}); err == nil {
		t.Error("负质量应报错")
	}
	if err := l.Load("B", 10, map[string]string{"x": "abc"}); err == nil {
		t.Error("非有理数质量应报错")
	}
	if err := l.AddWater("A", -5); err == nil {
		t.Error("负加水应报错")
	}
	if _, err := l.TransferBatch(nil); err == nil {
		t.Error("空批次应报错")
	}
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: -1}}); err == nil {
		t.Error("负转移体积应报错")
	}
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "Z", Volume: 1}}); err == nil {
		t.Error("不存在的目的容器应报错")
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
}

// Verify 能检出被篡改的状态（质量与份额不符、总量不守恒）。
func TestVerifyDetectsTampering(t *testing.T) {
	l := New()
	mustLoad(t, l, "A", 100, map[string]string{"salt": "1/3"})
	if err := l.AddContainer("B"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.TransferBatch([]Transfer{{From: "A", To: "B", Volume: 50}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	// 篡改质量但不改份额：份额和 != 质量。
	l.containers["B"].Solutes["salt"].Mass.SetInt64(99)
	if err := l.Verify(); err == nil {
		t.Fatal("应检出份额与质量不符")
	}
	// 同步篡改份额：总质量与初始记录不符。
	l.containers["B"].Solutes["salt"].Shares[1].SetInt64(99)
	if err := l.Verify(); err == nil {
		t.Fatal("应检出总量不守恒")
	}
}
