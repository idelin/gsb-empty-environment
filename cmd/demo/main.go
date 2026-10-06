// demo 执行三笔串行事务，枚举执行轨迹上的每个持久化切点，
// 在该点崩溃并恢复，打印恢复出的串行状态（S0..S3）。
package main

import (
	"bytes"
	"fmt"
	"strings"

	"walstore"
)

func pageData(s string) []byte {
	d := make([]byte, walstore.PageDataSize)
	copy(d, s)
	return d
}

func main() {
	tr := &walstore.Tracer{}
	walF := walstore.NewSimFile("wal", tr)
	dbF := walstore.NewSimFile("db", tr)
	st, err := walstore.Open(walF, dbF)
	if err != nil {
		panic(err)
	}
	st.OnCommit = func(id uint64) {
		tr.Ops = append(tr.Ops, walstore.Op{Kind: walstore.OpAck, Note: fmt.Sprintf("ack:%d", id)})
	}
	commit := func(pages map[uint32][]byte) {
		tx := st.Begin()
		for pid := uint32(0); pid < 3; pid++ {
			if d, ok := pages[pid]; ok {
				tx.Set(pid, d)
			}
		}
		if err := st.Commit(tx); err != nil {
			panic(err)
		}
		if err := st.Checkpoint(); err != nil {
			panic(err)
		}
	}
	fmt.Println("== 执行三笔事务 ==")
	fmt.Println("T1: p0=A1 p1=B1 | T2: p1=B2 p2=C2 | T3: p0=A3 p2=C3")
	commit(map[uint32][]byte{0: pageData("A1"), 1: pageData("B1")})
	commit(map[uint32][]byte{1: pageData("B2"), 2: pageData("C2")})
	commit(map[uint32][]byte{0: pageData("A3"), 2: pageData("C3")})

	fmt.Println("\n== 枚举持久化切点：崩溃 -> 恢复 -> 串行状态 ==")
	bad := 0
	for cut := 0; cut <= len(tr.Ops); cut++ {
		files := walstore.ReplayPrefix(tr.Ops, cut, -1)
		st2, err := walstore.Open(files["wal"], files["db"])
		if err != nil {
			panic(err)
		}
		var cells []string
		for pid := uint32(0); pid < 3; pid++ {
			p, err := st2.Get(pid)
			if err != nil {
				panic(err)
			}
			cells = append(cells, string(bytes.TrimRight(p, "\x00")))
		}
		state := fmt.Sprintf("p0=%-2s p1=%-2s p2=%-2s", cells[0], cells[1], cells[2])
		serial := serialOf(cells)
		if serial == "?" {
			bad++
		}
		last := "∅ (初始)"
		if cut > 0 {
			last = describe(tr.Ops[cut-1])
		}
		fmt.Printf("cut %2d/%-2d 最后落盘: %-18s => %s  [%s]\n", cut, len(tr.Ops), last, state, serial)
	}
	if bad > 0 {
		fmt.Printf("\n失败：%d 个切点恢复出非串行状态\n", bad)
	} else {
		fmt.Println("\n全部切点恢复为合法串行前缀 S0..S3，事务全有或全无。")
	}
}

func serialOf(cells []string) string {
	switch {
	case cells[0] == "" && cells[1] == "" && cells[2] == "":
		return "S0"
	case cells[0] == "A1" && cells[1] == "B1" && cells[2] == "":
		return "S1"
	case cells[0] == "A1" && cells[1] == "B2" && cells[2] == "C2":
		return "S2"
	case cells[0] == "A3" && cells[1] == "B2" && cells[2] == "C3":
		return "S3"
	}
	return "?"
}

func describe(op walstore.Op) string {
	kind := map[walstore.OpKind]string{walstore.OpWrite: "write", walstore.OpSync: "sync", walstore.OpAck: "ack"}[op.Kind]
	var b strings.Builder
	if op.Note != "" {
		fmt.Fprintf(&b, "%s:%s", kind, op.Note)
	} else {
		fmt.Fprintf(&b, "%s:%s@%d", kind, op.File, op.Off)
	}
	return b.String()
}
