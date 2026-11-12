package netsec

import (
	"errors"
	"testing"
)

// publicationProgram 构建消息传递范式：
//
//	T0: W(data)=secret; W(flag)=1
//	T1: R(flag); R(data)
//
// writeOrder 决定 flag 的发布语义，readOrder 决定其获取语义；普通对照场景
// 两者都传 OrderPlain。data 始终是普通访问。
func publicationProgram(t *testing.T, writeOrder, readOrder Order) (*Program, map[string]int) {
	t.Helper()
	b, err := NewProgramBuilder(2)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int{}
	ids["initData"], _ = b.AddInitialWrite("data", 0)
	ids["initFlag"], _ = b.AddInitialWrite("flag", 0)
	ids["wData"], _ = b.Add(0, Event{Loc: "data", IsWrite: true, Value: 42})
	ids["wFlag"], _ = b.Add(0, Event{Loc: "flag", IsWrite: true, Order: writeOrder, Value: 1})
	ids["rFlag"], _ = b.Add(1, Event{Loc: "flag", Order: readOrder})
	ids["rData"], _ = b.Add(1, Event{Loc: "data"})
	prog, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return prog, ids
}

// leakedReadFrom 建模读到未发布秘密的读自边组合：rData 已读到新秘密值，
// rFlag 却仍读到旧的发布标记（0）。发布/获取同步必须排除这种组合。
func leakedReadFrom(ids map[string]int) []ReadFrom {
	return []ReadFrom{
		{Reader: ids["rFlag"], Writer: ids["initFlag"]},
		{Reader: ids["rData"], Writer: ids["wData"]},
	}
}

// TestReleaseAcquirePublicationSafe 验证发布/获取语义下，读自边组合
// rData<-wData（读到新秘密）但 rFlag 仍读旧标记，与先行发生关系矛盾，
// 枚举器必须拒绝，即秘密不可能在发布标记可见前被读到。
func TestReleaseAcquirePublicationSafe(t *testing.T) {
	prog, ids := publicationProgram(t, OrderRelease, OrderAcquire)
	q := Query{
		Program:   prog,
		ReadFroms: leakedReadFrom(ids),
	}
	if _, err := Enumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("发布/获取下期望 ErrInfeasible，得到 %v", err)
	}
	if _, err := ReferenceEnumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("参考实现期望 ErrInfeasible，得到 %v", err)
	}
}

// TestPlainPublicationLeak 验证普通读写下允许读重排，消费者能在 flag 发布前
// 读到秘密 data 的新值，枚举器应给出最短失败见证。
func TestPlainPublicationLeak(t *testing.T) {
	prog, ids := publicationProgram(t, OrderPlain, OrderPlain)
	rf, err := ReadFromMap(Query{Program: prog, ReadFroms: leakedReadFrom(ids)})
	if err != nil {
		t.Fatal(err)
	}
	q := Query{
		Program:   prog,
		ReadFroms: leakedReadFrom(ids),
		Fail:      FailOnRead(rf, map[int]int{ids["rData"]: 42}),
	}

	res, err := Enumerate(q)
	if err != nil {
		t.Fatalf("普通读写下应存在合法执行: %v", err)
	}
	if len(res.Traces) != 6 {
		t.Fatalf("该读自边组合下应有 6 种连贯全序，得到 %d 条: %v", len(res.Traces), res.Traces)
	}

	witness, err := ShortestWitness(q)
	if err != nil {
		t.Fatalf("应找到断言失败见证: %v", err)
	}
	if witness == nil {
		t.Fatal("普通读写下必须存在读到秘密缓冲区的见证")
	}
	if witness.FailAt != 2 || len(witness.Prefix) != 2 {
		t.Fatalf("最短失败前缀长度应为 2（R(data) 读到秘密即失败），得到 FailAt=%d", witness.FailAt)
	}
	if got := witness.Prefix[1]; got != ids["rData"] {
		t.Fatalf("最短前缀的第二个事件应为 R(data)=%d，得到 %d", ids["rData"], got)
	}
	// 完整见证必须包含全部真实事件且前缀是其头部。
	if len(witness.Trace) != 4 {
		t.Fatalf("完整见证应包含 4 个事件，得到 %d", len(witness.Trace))
	}
}

// TestFenceBlocksLeak 验证发布屏障 + 获取屏障同样建立先行发生关系，
// 阻止读到未发布的秘密。
func TestFenceBlocksLeak(t *testing.T) {
	b, err := NewProgramBuilder(2)
	if err != nil {
		t.Fatal(err)
	}
	b.AddInitialWrite("data", 0)
	b.AddInitialWrite("flag", 0)
	b.Add(0, Event{Loc: "data", IsWrite: true, Value: 42})
	b.Add(0, Event{IsFence: true, Order: OrderRelease})
	wFlag, _ := b.Add(0, Event{Loc: "flag", IsWrite: true, Value: 1})
	rFlag, _ := b.Add(1, Event{Loc: "flag"})
	b.Add(1, Event{IsFence: true, Order: OrderAcquire})
	rData, _ := b.Add(1, Event{Loc: "data"})
	prog, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}

	q := Query{
		Program: prog,
		ReadFroms: []ReadFrom{
			// rFlag 读到发布写从而触发两侧围栏同步；围栏 HB 要求 wData 先于
			// rData，因此 rData 不可能仍读到 data 的旧初始值——下面的读自边
			// 组合与此矛盾，应被先行发生关系排除。
			{Reader: rFlag, Writer: wFlag},
			{Reader: rData, Writer: prog.Initial["data"]},
		},
	}
	if _, err := Enumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("屏障同步下期望 ErrInfeasible，得到 %v", err)
	}
	if _, err := ReferenceEnumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("参考实现屏障同步下期望 ErrInfeasible，得到 %v", err)
	}

	// 去掉屏障后同样的读自边应当可行，证明屏障是不可缺少的一环。
	b2, _ := NewProgramBuilder(2)
	b2.AddInitialWrite("data", 0)
	b2.AddInitialWrite("flag", 0)
	wData2, _ := b2.Add(0, Event{Loc: "data", IsWrite: true, Value: 42})
	b2.Add(0, Event{Loc: "flag", IsWrite: true, Value: 1})
	rFlag2, _ := b2.Add(1, Event{Loc: "flag"})
	rData2, _ := b2.Add(1, Event{Loc: "data"})
	prog2, _ := b2.Build()
	q2 := Query{
		Program: prog2,
		ReadFroms: []ReadFrom{
			{Reader: rFlag2, Writer: prog2.Initial["flag"]},
			{Reader: rData2, Writer: wData2},
		},
	}
	if _, err := Enumerate(q2); err != nil {
		t.Fatalf("无屏障普通读写下应存在合法执行: %v", err)
	}
	if _, err := ReferenceEnumerate(q2); err != nil {
		t.Fatalf("参考实现无屏障下应存在合法执行: %v", err)
	}
}

// TestDifferentialAgainstReference 在小规模程序上对每个读自边组合比较两个
// 实现的合法执行集合，必须完全一致。
func TestDifferentialAgainstReference(t *testing.T) {
	prog, ids := publicationProgram(t, OrderPlain, OrderPlain)
	rfCases := [][]ReadFrom{
		nil,
		{{Reader: ids["rFlag"], Writer: ids["wFlag"]}},
		{{Reader: ids["rData"], Writer: ids["wData"]}},
		leakedReadFrom(ids),
	}
	for i, rf := range rfCases {
		q := Query{Program: prog, ReadFroms: rf}
		main, mErr := Enumerate(q)
		ref, rErr := ReferenceEnumerate(q)
		if !assertSameOutcome(t, i, main, mErr, ref, rErr) {
			return
		}
	}
}

// TestDifferentialReleaseAcquire 对带同步语义的程序做差分，并覆盖不可行情景。
func TestDifferentialReleaseAcquire(t *testing.T) {
	prog, ids := publicationProgram(t, OrderRelease, OrderAcquire)
	q := Query{Program: prog, ReadFroms: leakedReadFrom(ids)}
	if _, err := Enumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("主枚举器应判定不可行，得到 %v", err)
	}
	if _, err := ReferenceEnumerate(q); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("参考实现应判定不可行，得到 %v", err)
	}

	// 只让 rFlag 读到发布写、rData 仍读初始写：可行，两实现结果一致。
	q2 := Query{Program: prog, ReadFroms: []ReadFrom{
		{Reader: ids["rFlag"], Writer: ids["wFlag"]},
	}}
	main, mErr := Enumerate(q2)
	ref, rErr := ReferenceEnumerate(q2)
	if !assertSameOutcome(t, 99, main, mErr, ref, rErr) {
		return
	}
}

// TestBuilderLimits 校验线程与每线程事件数上限。
func TestBuilderLimits(t *testing.T) {
	if _, err := NewProgramBuilder(MaxThreads + 1); err == nil {
		t.Fatal("超线程数应报错")
	}
	b, _ := NewProgramBuilder(1)
	b.AddInitialWrite("x", 0)
	for i := 0; i < MaxEventsPerThread; i++ {
		if _, err := b.Add(0, Event{Loc: "x"}); err != nil {
			t.Fatalf("第 %d 个事件应被接受: %v", i, err)
		}
	}
	if _, err := b.Add(0, Event{Loc: "x"}); err == nil {
		t.Fatal("超过每线程事件上限应报错")
	}
}

// TestThreeThreadProgram 验证三线程程序能被枚举，且两个实现结果一致。
func TestThreeThreadProgram(t *testing.T) {
	b, _ := NewProgramBuilder(3)
	b.AddInitialWrite("x", 0)
	w1, _ := b.Add(0, Event{Loc: "x", IsWrite: true, Value: 1})
	r2, _ := b.Add(1, Event{Loc: "x"})
	r3, _ := b.Add(2, Event{Loc: "x"})
	prog, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	q := Query{Program: prog, ReadFroms: []ReadFrom{
		{Reader: r2, Writer: w1},
		{Reader: r3, Writer: prog.Initial["x"]},
	}}
	main, err := Enumerate(q)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := ReferenceEnumerate(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(NormalizeTraces(main.Traces)) != len(NormalizeTraces(ref.Traces)) {
		t.Fatal("三线程程序两实现执行数量不一致")
	}
}

func traceEqual(a, b Trace) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// assertSameOutcome 比较主枚举器与参考实现对同一查询的结果：错误类别与
// 规范化后的执行集合都必须一致。
func assertSameOutcome(t *testing.T, caseID int, main *Result, mErr error, ref *Result, rErr error) bool {
	t.Helper()
	if (mErr != nil) != (rErr != nil) {
		t.Fatalf("case %d 错误不一致: 主=%v 参考=%v", caseID, mErr, rErr)
		return false
	}
	if mErr != nil {
		if !errors.Is(mErr, ErrInfeasible) || !errors.Is(rErr, ErrInfeasible) {
			t.Fatalf("case %d 出现非预期错误: 主=%v 参考=%v", caseID, mErr, rErr)
		}
		return true
	}
	got := NormalizeTraces(main.Traces)
	want := NormalizeTraces(ref.Traces)
	if len(got) != len(want) {
		t.Fatalf("case %d 执行数量不一致: %d != %d", caseID, len(got), len(want))
	}
	for k := range got {
		if !traceEqual(got[k], want[k]) {
			t.Fatalf("case %d 第 %d 条执行不一致: %v != %v", caseID, k, got[k], want[k])
		}
	}
	return true
}
