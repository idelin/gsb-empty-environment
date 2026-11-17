package netsec

import "testing"

func ids(res *Result) map[string]Certainty {
	out := map[string]Certainty{}
	for _, r := range res.DangerousWrites {
		out[r.NodeID] = r.Certainty
	}
	return out
}

func analyzeOrFail(t *testing.T, prog *Program) *Result {
	t.Helper()
	res, err := Analyze(prog)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}
	return res
}

// 直接写共享原型：确定危险，且证据指向 P 来源。
func TestDirectSharedProtoWrite(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "k", Value: StrProto},
		&Load{ID: "n3", Dest: "p", Obj: "o", Key: "k"},
		&Const{ID: "n4", Dest: "f", Value: StrA},
		&Const{ID: "n5", Dest: "v", Value: StrB},
		&Store{ID: "n6", Obj: "p", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	got := ids(res)
	if got["n6"] != Definite {
		t.Fatalf("want definite n6, got %v", got)
	}
	if len(res.DangerousWrites[0].ReceiverTrace) == 0 {
		t.Fatal("missing receiver data-flow evidence")
	}

	conc, err := ExploreConcrete(prog, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(conc.DangerNodes) != 1 || conc.DangerNodes[0] != "n6" {
		t.Fatalf("concrete danger mismatch: %v", conc.DangerNodes)
	}
}

// 独立对象自身字段写：不得误报。
func TestBenignIndependentObjectWrite(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "f", Value: StrA},
		&Const{ID: "n3", Dest: "v", Value: StrB},
		&Store{ID: "n4", Obj: "o", Key: "f", Val: "v"},
		&Store{ID: "n5", Obj: "o", Key: "f2", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	if len(res.DangerousWrites) != 0 {
		t.Fatalf("benign writes must not be flagged: %+v", res.DangerousWrites)
	}
}

// 外部字符串作为键：__proto__ 在取值域内，保守报告为 definite
// （接收对象确实是 P，只是写入字段随外部值变化）。
func TestExternalKeyToSharedProto(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "protoKey", Value: StrProto},
		&Load{ID: "n3", Dest: "p", Obj: "o", Key: "protoKey"},
		&External{ID: "n4", Dest: "k"},
		&Const{ID: "n5", Dest: "v", Value: StrB},
		&Store{ID: "n6", Obj: "p", Key: "k", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	got := ids(res)
	if got["n6"] != Definite {
		t.Fatalf("want definite external-key write, got %v", got)
	}
	r := res.DangerousWrites[0]
	if len(r.PossibleKeys) != 5 {
		t.Fatalf("want all 5 possible keys, got %v", r.PossibleKeys)
	}
}

// constructor.prototype 指回 P，经两层字段读到 P 后写入：确定危险。
func TestConstructorPrototypePollution(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "ck", Value: StrConstructor},
		&Load{ID: "n3", Dest: "c", Obj: "o", Key: "ck"},
		&Const{ID: "n4", Dest: "pk", Value: StrPrototype},
		&Load{ID: "n5", Dest: "p", Obj: "c", Key: "pk"},
		&Const{ID: "n6", Dest: "f", Value: StrA},
		&Const{ID: "n7", Dest: "v", Value: StrB},
		&Store{ID: "n8", Obj: "p", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	got := ids(res)
	if got["n8"] != Definite {
		t.Fatalf("want definite n8, got %v", got)
	}

	conc, err := ExploreConcrete(prog, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(conc.DangerNodes) != 1 {
		t.Fatalf("concrete mismatch: %v", conc.DangerNodes)
	}
}

// 条件分支造成 may 别名：一条路径接收者是 P，另一条是独立对象，
// 合并后只能保守报告 possible。
func TestBranchMergePossible(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Alloc{ID: "n2", Dest: "safe"},
		&Const{ID: "n3", Dest: "protoKey", Value: StrProto},
		&Load{ID: "n4", Dest: "p", Obj: "o", Key: "protoKey"},
		&If{ID: "n5", Then: []Node{
			&Alias{ID: "n6", Dest: "x", Src: "p"},
		}, Else: []Node{
			&Alias{ID: "n7", Dest: "x", Src: "o"},
		}},
		&Const{ID: "n8", Dest: "f", Value: StrA},
		&Const{ID: "n9", Dest: "v", Value: StrB},
		&Store{ID: "n10", Obj: "x", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	got := ids(res)
	if got["n10"] != Possible {
		t.Fatalf("want possible n10 after branch merge, got %v", got)
	}

	conc, err := ExploreConcrete(prog, 1000)
	if err != nil || len(conc.DangerNodes) != 1 || conc.DangerNodes[0] != "n10" {
		t.Fatalf("concrete should confirm one dangerous branch, got %v err=%v", conc, err)
	}
}

// 两个分支接收者都是 P：合并后 must 仍为 P，应保持 definite。
func TestBothBranchesDefinite(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "protoKey", Value: StrProto},
		&Load{ID: "n3", Dest: "p", Obj: "o", Key: "protoKey"},
		&If{ID: "n4", Then: []Node{
			&Alias{ID: "n5", Dest: "x", Src: "p"},
		}, Else: []Node{
			&Alias{ID: "n6", Dest: "x", Src: "p"},
		}},
		&Const{ID: "n7", Dest: "f", Value: StrA},
		&Const{ID: "n8", Dest: "v", Value: StrB},
		&Store{ID: "n9", Obj: "x", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	if got := ids(res)["n9"]; got != Definite {
		t.Fatalf("want definite, got %v", got)
	}
}

// 循环内把对象原型改写为另一个独立对象：独立对象自身字段写与经
// __proto__ 读到的独立对象字段写都不得误报，且与具体执行结论一致。
func TestLoopProtoRewriteBenign(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Alloc{ID: "n2", Dest: "q"},
		&Const{ID: "n3", Dest: "protoKey", Value: StrProto},
		&Loop{ID: "n4", Count: 3, Body: []Node{
			&Store{ID: "n5", Obj: "o", Key: "protoKey", Val: "q"},
		}},
		&Load{ID: "n6", Dest: "op", Obj: "o", Key: "protoKey"},
		&Const{ID: "n7", Dest: "f", Value: StrA},
		&Const{ID: "n8", Dest: "v", Value: StrB},
		&Store{ID: "n9", Obj: "op", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	if len(res.DangerousWrites) != 0 {
		t.Fatalf("rewrite to another allocated object is benign: %+v", res.DangerousWrites)
	}

	conc, err := ExploreConcrete(prog, 1000)
	if err != nil || len(conc.DangerNodes) != 0 {
		t.Fatalf("concrete mismatch: %v err=%v", conc, err)
	}
}

// 循环内把对象原型改写为共享原型（__proto__ 自指 P），后续字段写经
// 显式读取 __proto__ 拿到 P 再写入：确定危险，且与具体穷举结论一致。
func TestLoopProtoRewritePollution(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "protoKey", Value: StrProto},
		&Loop{ID: "n4", Count: 2, Body: []Node{
			&Load{ID: "n3", Dest: "p", Obj: "o", Key: "protoKey"},
			&Store{ID: "n5", Obj: "o", Key: "protoKey", Val: "p"},
		}},
		&Load{ID: "n9", Dest: "p2", Obj: "o", Key: "protoKey"},
		&Const{ID: "n6", Dest: "f", Value: StrA},
		&Const{ID: "n7", Dest: "v", Value: StrB},
		&Store{ID: "n8", Obj: "p2", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	got := ids(res)
	if got["n5"] != Definite || got["n8"] != Definite {
		t.Fatalf("want definite n5 and n8, got %v (all: %v)", got, res.DangerousWrites)
	}

	conc, err := ExploreConcrete(prog, 1000)
	if err != nil || len(conc.DangerNodes) != 2 {
		t.Fatalf("concrete mismatch: %v err=%v", conc, err)
	}
}

// 循环中重复分配点是多对象抽象：写其字段不能被当成写 P。
func TestLoopRepeatedAllocNoFalsePositive(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Const{ID: "n1", Dest: "f", Value: StrA},
		&Const{ID: "n2", Dest: "v", Value: StrB},
		&Loop{ID: "n3", Count: 4, Body: []Node{
			&Alloc{ID: "n4", Dest: "o"},
			&Store{ID: "n5", Obj: "o", Key: "f", Val: "v"},
		}},
	}}
	res := analyzeOrFail(t, prog)
	if len(res.DangerousWrites) != 0 {
		t.Fatalf("repeated alloc writes are benign: %+v", res.DangerousWrites)
	}
}

// 外部键含 __proto__：对独立对象是原型改写，对 P 则是危险写。
func TestExternalKeyProtoWriteOnP(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "protoKey", Value: StrProto},
		&Load{ID: "n3", Dest: "p", Obj: "o", Key: "protoKey"},
		&External{ID: "n4", Dest: "k"},
		&Alloc{ID: "n5", Dest: "v"},
		&Store{ID: "n6", Obj: "p", Key: "k", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	if got := ids(res)["n6"]; got != Definite {
		t.Fatalf("want definite n6, got %v", got)
	}

	conc, err := ExploreConcrete(prog, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(conc.DangerNodes) != 1 {
		t.Fatalf("concrete mismatch: %v", conc.DangerNodes)
	}
}

// 字符串变量作为接收者的 Store 无对象语义，不应产生报告。
func TestStringReceiverIgnored(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Const{ID: "n1", Dest: "s", Value: StrA},
		&Const{ID: "n2", Dest: "f", Value: StrB},
		&Const{ID: "n3", Dest: "v", Value: StrA},
		&Store{ID: "n4", Obj: "s", Key: "f", Val: "v"},
	}}
	res := analyzeOrFail(t, prog)
	if len(res.DangerousWrites) != 0 {
		t.Fatalf("string receiver must be ignored: %+v", res.DangerousWrites)
	}
}

// 端到端：外部字符串控制写入字段，接收者经 __proto__ 解析为共享原型。
// 报告必须携带完整的接收者/键数据流证据。
func TestEndToEndExternalInputPollutionChain(t *testing.T) {
	prog := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "n1", Dest: "o"},
		&Const{ID: "n2", Dest: "pk", Value: StrProto},
		&Load{ID: "n3", Dest: "p", Obj: "o", Key: "pk"},
		&External{ID: "n4", Dest: "field"},
		&External{ID: "n5", Dest: "payload"},
		&Store{ID: "n6", Obj: "p", Key: "field", Val: "payload"},
	}}
	res := analyzeOrFail(t, prog)
	if len(res.DangerousWrites) != 1 {
		t.Fatalf("want exactly one report, got %+v", res.DangerousWrites)
	}
	r := res.DangerousWrites[0]
	if r.NodeID != "n6" || r.Certainty != Definite {
		t.Fatalf("unexpected report: %+v", r)
	}
	foundExternalEvidence := false
	for _, ev := range r.KeyTrace {
		if ev.NodeID == "n4" {
			foundExternalEvidence = true
		}
	}
	if !foundExternalEvidence {
		t.Fatalf("key trace missing external-source evidence: %+v", r.KeyTrace)
	}

	conc, err := ExploreConcrete(prog, 5000)
	if err != nil {
		t.Fatal(err)
	}
	// 外部键取到 __proto__ 时改 P 的原型，取其他值时写 P 自身字段，均危险。
	if len(conc.DangerNodes) != 1 || conc.DangerNodes[0] != "n6" {
		t.Fatalf("concrete mismatch: %v", conc.DangerNodes)
	}
	if conc.Paths < 25 {
		t.Fatalf("expected full 5x5 external exploration, got %d paths", conc.Paths)
	}
}
