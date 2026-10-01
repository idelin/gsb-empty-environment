// Command canon-demo demonstrates canonical encoding of tree values and
// of rooted graphs with shared references and cycles.
package main

import (
	"bytes"
	"errors"
	"fmt"

	"canon"
)

func main() {
	// 1. Tree values: key insertion order does not matter.
	obj1, _ := canon.NewObject(
		canon.KV{Key: "name", Value: canon.String("alice")},
		canon.KV{Key: "id", Value: canon.Int(1)},
		canon.KV{Key: "scores", Value: canon.Array{canon.Int(90), canon.Int(85)}},
		canon.KV{Key: "active", Value: canon.Bool(true)},
	)
	obj2, _ := canon.NewObject(
		canon.KV{Key: "active", Value: canon.Bool(true)},
		canon.KV{Key: "scores", Value: canon.Array{canon.Int(90), canon.Int(85)}},
		canon.KV{Key: "id", Value: canon.Int(1)},
		canon.KV{Key: "name", Value: canon.String("alice")},
	)
	enc1, _ := canon.Encode(obj1)
	enc2, _ := canon.Encode(obj2)
	fmt.Printf("object encoding (%d bytes): %x\n", len(enc1), enc1)
	fmt.Printf("different key insertion order, same bytes: %v\n", bytes.Equal(enc1, enc2))
	d, _ := canon.Digest(obj1)
	fmt.Printf("sha-256 digest for external signing: %x\n\n", d)

	// 2. Distinct types never collide.
	ei, _ := canon.Encode(canon.Int(1))
	es, _ := canon.Encode(canon.String("1"))
	fmt.Printf("Int(1) != String(\"1\"): %v\n\n", !bytes.Equal(ei, es))

	// 3. Rooted graph with a shared node and a cycle back to the root.
	build := func() *canon.GNode {
		shared := &canon.GNode{Label: canon.String("leaf")}
		a := &canon.GNode{Label: canon.String("a")}
		b := &canon.GNode{Label: canon.String("b")}
		root := &canon.GNode{Label: canon.String("root")}
		a.Out = []*canon.GNode{shared}
		b.Out = []*canon.GNode{shared}
		shared.Out = []*canon.GNode{root} // cycle
		root.Out = []*canon.GNode{a, b}
		return root
	}
	g1, _ := canon.EncodeGraph(build(), 0)
	g2, _ := canon.EncodeGraph(build(), 0) // independently rebuilt graph
	fmt.Printf("graph encoding (%d bytes): %x\n", len(g1), g1)
	fmt.Printf("isomorphic graph, different construction: same bytes: %v\n", bytes.Equal(g1, g2))
	rt, _ := canon.DecodeGraph(g1)
	re, _ := canon.EncodeGraph(rt, 0)
	fmt.Printf("decode/re-encode round trip stable: %v\n\n", bytes.Equal(g1, re))

	// 4. Bounded search: symmetric graphs need individualization, and an
	//    exhausted budget is reported explicitly as incomplete.
	cycle := func(n int) *canon.GNode {
		nodes := make([]*canon.GNode, n)
		for i := range nodes {
			nodes[i] = &canon.GNode{Label: canon.Null{}}
		}
		for i := range nodes {
			nodes[i].Out = []*canon.GNode{nodes[(i+1)%n]}
		}
		return nodes[0]
	}
	_, err := canon.EncodeGraph(cycle(8), 2)
	fmt.Printf("uniform 8-cycle, budget=2: %v\n", err)
	fmt.Printf("explicitly incomplete: %v\n", errors.Is(err, canon.ErrBudgetExceeded))
	enc8, _ := canon.EncodeGraph(cycle(8), 0)
	fmt.Printf("uniform 8-cycle, default budget: ok (%d bytes)\n", len(enc8))
}
