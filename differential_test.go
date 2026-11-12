package netsec

import "testing"

// allReadFromChoices 枚举两个读各自的写来源组合，用于穷举差分测试。
func allReadFromChoices(reads, writers []int) [][]ReadFrom {
	var out [][]ReadFrom
	var rec func(idx int, acc []ReadFrom)
	rec = func(idx int, acc []ReadFrom) {
		if idx == len(reads) {
			out = append(out, append([]ReadFrom(nil), acc...))
			return
		}
		for _, w := range writers {
			rec(idx+1, append(acc, ReadFrom{Reader: reads[idx], Writer: w}))
		}
	}
	rec(0, nil)
	return out
}

// TestExhaustiveRFDifferential 对普通读写 MP 程序穷举所有读自边组合，逐组
// 比较主枚举器与全排列参考实现的可行性与执行集合。
func TestExhaustiveRFDifferential(t *testing.T) {
	prog, ids := publicationProgram(t, OrderPlain, OrderPlain)
	flagWrites := []int{prog.Initial["flag"], ids["wFlag"]}
	dataWrites := []int{prog.Initial["data"], ids["wData"]}

	caseID := 0
	for _, fw := range flagWrites {
		for _, dw := range dataWrites {
			q := Query{Program: prog, ReadFroms: []ReadFrom{
				{Reader: ids["rFlag"], Writer: fw},
				{Reader: ids["rData"], Writer: dw},
			}}
			main, mErr := Enumerate(q)
			ref, rErr := ReferenceEnumerate(q)
			if !assertSameOutcome(t, caseID, main, mErr, ref, rErr) {
				return
			}
			caseID++
		}
	}
}

// TestFenceDifferential 对带屏障程序穷举读自边组合做差分。
func TestFenceDifferential(t *testing.T) {
	b, _ := NewProgramBuilder(2)
	b.AddInitialWrite("data", 0)
	b.AddInitialWrite("flag", 0)
	wData, _ := b.Add(0, Event{Loc: "data", IsWrite: true, Value: 42})
	b.Add(0, Event{IsFence: true, Order: OrderRelease})
	wFlag, _ := b.Add(0, Event{Loc: "flag", IsWrite: true, Value: 1})
	rFlag, _ := b.Add(1, Event{Loc: "flag"})
	b.Add(1, Event{IsFence: true, Order: OrderAcquire})
	rData, _ := b.Add(1, Event{Loc: "data"})
	prog, _ := b.Build()

	caseID := 0
	for _, fw := range []int{prog.Initial["flag"], wFlag} {
		for _, dw := range []int{prog.Initial["data"], wData} {
			q := Query{Program: prog, ReadFroms: []ReadFrom{
				{Reader: rFlag, Writer: fw},
				{Reader: rData, Writer: dw},
			}}
			main, mErr := Enumerate(q)
			ref, rErr := ReferenceEnumerate(q)
			if !assertSameOutcome(t, caseID, main, mErr, ref, rErr) {
				return
			}
			caseID++
		}
	}
}

// TestReferenceLimit 校验参考实现的规模保护。
func TestReferenceLimit(t *testing.T) {
	b, _ := NewProgramBuilder(MaxThreads)
	b.AddInitialWrite("x", 0)
	// 每线程放 4 个事件，总数超过 MaxReferenceEvents。
	for th := 0; th < MaxThreads; th++ {
		for i := 0; i < MaxEventsPerThread; i++ {
			b.Add(th, Event{Loc: "x"})
		}
	}
	prog, _ := b.Build()
	if _, err := ReferenceEnumerate(Query{Program: prog}); err != ErrLimitExceeded {
		t.Fatalf("超限应返回 ErrLimitExceeded，得到 %v", err)
	}
	// 主枚举器不受全排列上限约束，仍可工作。
	if _, err := Enumerate(Query{Program: prog, Limit: 1}); err != nil {
		t.Fatalf("主枚举器应能处理上限内程序: %v", err)
	}
}

// TestInvalidInputs 覆盖常见非法输入。
func TestInvalidInputs(t *testing.T) {
	if _, err := Enumerate(Query{}); err == nil {
		t.Fatal("空程序应报错")
	}
	b, _ := NewProgramBuilder(1)
	b.AddInitialWrite("x", 0)
	r, _ := b.Add(0, Event{Loc: "x"})
	prog, _ := b.Build()

	// 读自边写端不是写事件。
	if _, err := Enumerate(Query{
		Program:   prog,
		ReadFroms: []ReadFrom{{Reader: r, Writer: r}},
	}); err == nil {
		t.Fatal("非法读自边应报错")
	}

	// ShortestWitness 缺少谓词。
	if _, err := ShortestWitness(Query{Program: prog}); err == nil {
		t.Fatal("缺少 Fail 谓词应报错")
	}
}
