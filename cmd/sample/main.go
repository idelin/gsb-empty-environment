// Command sample demonstrates encoding and decoding a cyclic object
// graph under resource limits, including rejection of hostile inputs.
package main

import (
	"fmt"
	"strings"

	"graphdec"
)

// render prints a graph with cycle protection.
func render(v *graphdec.Value) string {
	var sb strings.Builder
	seen := map[*graphdec.Value]int{}
	count := 0
	var walk func(v *graphdec.Value, depth int)
	walk = func(v *graphdec.Value, depth int) {
		pad := strings.Repeat("  ", depth)
		if id, ok := seen[v]; ok {
			fmt.Fprintf(&sb, "%s-> ref #%d\n", pad, id)
			return
		}
		seen[v] = count
		id := count
		count++
		switch v.Kind {
		case graphdec.KindNull:
			fmt.Fprintf(&sb, "%s#%d null\n", pad, id)
		case graphdec.KindBool:
			fmt.Fprintf(&sb, "%s#%d bool %v\n", pad, id, v.Bool)
		case graphdec.KindInt:
			fmt.Fprintf(&sb, "%s#%d int %d\n", pad, id, v.Int)
		case graphdec.KindString:
			fmt.Fprintf(&sb, "%s#%d string %q\n", pad, id, v.Str)
		case graphdec.KindBytes:
			fmt.Fprintf(&sb, "%s#%d bytes %x\n", pad, id, v.Bytes)
		case graphdec.KindList:
			fmt.Fprintf(&sb, "%s#%d list\n", pad, id)
			for _, c := range v.List {
				walk(c, depth+1)
			}
		case graphdec.KindMap:
			fmt.Fprintf(&sb, "%s#%d map\n", pad, id)
			for _, e := range v.Map {
				walk(e.Key, depth+1)
				walk(e.Value, depth+1)
			}
		}
	}
	walk(v, 0)
	return sb.String()
}

func main() {
	// A document graph with a shared author node and a cycle
	// (the "related" list points back at the document root).
	author := &graphdec.Value{Kind: graphdec.KindString, Str: "ada"}
	related := &graphdec.Value{Kind: graphdec.KindList}
	doc := &graphdec.Value{Kind: graphdec.KindMap, Map: []graphdec.MapEntry{
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "title"}, Value: &graphdec.Value{Kind: graphdec.KindString, Str: "limits"}},
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "version"}, Value: &graphdec.Value{Kind: graphdec.KindInt, Int: 3}},
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "draft"}, Value: &graphdec.Value{Kind: graphdec.KindBool, Bool: false}},
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "author"}, Value: author},
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "reviewer"}, Value: author},
		{Key: &graphdec.Value{Kind: graphdec.KindString, Str: "related"}, Value: related},
	}}
	related.List = []*graphdec.Value{doc} // cycle back to the root
	data, err := graphdec.Marshal(doc)
	if err != nil {
		panic(err)
	}
	fmt.Printf("encoded: %d bytes\n", len(data))
	dec := &graphdec.Decoder{Limits: graphdec.Limits{
		MaxBytes: 1 << 20, MaxNodes: 1000, MaxRefs: 2000,
		MaxDepth: 16, MaxCycleNodes: 8, MaxPendingRefs: 64,
	}}
	v, err := dec.Decode(data)
	if err != nil {
		panic(err)
	}
	fmt.Print("decoded graph:\n", render(v))
	// Hostile input 1: a chain deeper than the depth budget.
	deep := &graphdec.Value{Kind: graphdec.KindNull}
	for i := 0; i < 100; i++ {
		deep = &graphdec.Value{Kind: graphdec.KindList, List: []*graphdec.Value{deep}}
	}
	deepData, _ := graphdec.Marshal(deep)
	if v, err := dec.Decode(deepData); err != nil {
		fmt.Printf("deep chain rejected (value leaked: %v): %v\n", v != nil, err)
	}
	// Hostile input 2: tiny input, huge fan-out against the ref budget.
	leaf := &graphdec.Value{Kind: graphdec.KindNull}
	wide := &graphdec.Value{Kind: graphdec.KindList, List: make([]*graphdec.Value, 5000)}
	for i := range wide.List {
		wide.List[i] = leaf
	}
	wideData, _ := graphdec.Marshal(wide)
	if v, err := dec.Decode(wideData); err != nil {
		fmt.Printf("huge fan-out rejected (%d bytes input, value leaked: %v): %v\n", len(wideData), v != nil, err)
	}
	// After limit failures the decoder keeps working.
	if _, err := dec.Decode(data); err != nil {
		panic(err)
	}
	fmt.Println("decoder still usable after rejected inputs: ok")
}
