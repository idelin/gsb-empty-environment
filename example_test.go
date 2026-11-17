package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q-002"
)

// 演示：经 o.__proto__ 取得共享原型后写入，分析器报告确定危险写。
func ExampleAnalyze() {
	prog := &netsec.Program{ID: "demo", Body: []netsec.Node{
		&netsec.Alloc{ID: "n1", Dest: "o"},
		&netsec.Const{ID: "n2", Dest: "protoKey", Value: netsec.StrProto},
		&netsec.Load{ID: "n3", Dest: "p", Obj: "o", Key: "protoKey"},
		&netsec.Const{ID: "n4", Dest: "field", Value: netsec.StrA},
		&netsec.Const{ID: "n5", Dest: "val", Value: netsec.StrB},
		&netsec.Store{ID: "n6", Obj: "p", Key: "field", Val: "val"},
	}}

	res, err := netsec.Analyze(prog)
	if err != nil {
		panic(err)
	}
	for _, dw := range res.DangerousWrites {
		fmt.Printf("%s %s keys=%v\n", dw.NodeID, dw.Certainty, dw.PossibleKeys)
	}
	// Output:
	// n6 definite keys=[a]
}

// 演示：独立对象自身字段写不产生任何报告。
func ExampleAnalyze_benign() {
	prog := &netsec.Program{ID: "demo", Body: []netsec.Node{
		&netsec.Alloc{ID: "n1", Dest: "o"},
		&netsec.Const{ID: "n2", Dest: "field", Value: netsec.StrA},
		&netsec.Const{ID: "n3", Dest: "val", Value: netsec.StrB},
		&netsec.Store{ID: "n4", Obj: "o", Key: "field", Val: "val"},
	}}

	res, err := netsec.Analyze(prog)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(res.DangerousWrites))
	// Output:
	// 0
}
