// replay 从 JSON 夹具回放本地合成的遥测事件，打印组装出的树与结构问题。
//
// 用法: go run ./cmd/replay fixtures/late_root.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"traceasm"
)

type fixture struct {
	TTLMillis int64             `json:"ttl_ms"`
	Events    []assembler.Event `json:"events"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: replay <fixture.json>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read fixture:", err)
		os.Exit(1)
	}
	var fx fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		fmt.Fprintln(os.Stderr, "parse fixture:", err)
		os.Exit(1)
	}
	ttl := time.Duration(fx.TTLMillis) * time.Millisecond
	asm := assembler.New(ttl)

	var last time.Time
	for _, ev := range fx.Events {
		asm.Ingest(ev)
		if ev.At.After(last) {
			last = ev.At
		}
	}
	// 回放结束：推进时钟越过 TTL，结算所有缺父超时。
	asm.AdvanceTo(last.Add(2 * ttl))

	traceIDs := map[string]bool{}
	for _, ev := range fx.Events {
		traceIDs[ev.TraceID] = true
	}
	ids := make([]string, 0, len(traceIDs))
	for id := range traceIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		fmt.Printf("trace %s\n", id)
		for _, root := range asm.Roots(id) {
			printTree(root, 1)
		}
		for _, s := range asm.Orphans(id) {
			fmt.Printf("  (orphan) %s %q waiting for parent %s\n", s.SpanID, s.Name, s.ParentID)
		}
		for _, s := range asm.Unresolved(id) {
			reason := "missing parent"
			if s.Cyclic() {
				reason = "structural cycle at parent"
			}
			fmt.Printf("  (unresolved) %s %q %s %s\n", s.SpanID, s.Name, reason, s.ParentID)
		}
	}

	fmt.Println("issues:")
	if len(asm.Issues()) == 0 {
		fmt.Println("  (none)")
	}
	for _, is := range asm.Issues() {
		fmt.Println(" ", is)
	}
}

func printTree(s *assembler.Span, depth int) {
	indent := ""
	for i := 0; i < depth; i++ {
		indent += "  "
	}
	fmt.Printf("%s%s %q\n", indent, s.SpanID, s.Name)
	for _, c := range s.Children {
		printTree(c, depth+1)
	}
}
