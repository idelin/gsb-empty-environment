package netsec

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const tenantA = "tenant-a"

func capGraph(tokens ...string) *Graph {
	nodes := make([]Node, len(tokens))
	for i, tok := range tokens {
		nodes[i] = Node{Kind: KindCap, Token: tok}
	}
	if len(tokens) == 1 {
		return &Graph{Nodes: nodes}
	}
	items := make([]int, len(nodes))
	for i := range nodes {
		items[i] = i
	}
	return &Graph{Nodes: append([]Node{{Kind: KindArray, Items: items}}, nodes...)}
}

func TestDecodeSharedAndCycleIdentity(t *testing.T) {
	reg := NewRegistry()
	// 0: array [1, 1, 2]；1: 字符串；2: array [0]（指回根，成环）
	g := &Graph{Nodes: []Node{
		{Kind: KindArray, Items: []int{1, 1, 2}},
		{Kind: KindString, Str: "x"},
		{Kind: KindArray, Items: []int{0}},
	}}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, g)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	defer res.Release()

	root := res.Root
	if root.Items[0] != root.Items[1] {
		t.Fatal("shared child lost identity")
	}
	if root.Items[0].Str != "x" {
		t.Fatalf("shared child Str = %q, want %q", root.Items[0].Str, "x")
	}
	if root.Items[2].Items[0] != root {
		t.Fatal("cycle back-reference lost root identity")
	}
}

func TestDecodeDictAndRefAlias(t *testing.T) {
	reg := NewRegistry()
	// 0: dict {a: 2, b: 3}；2: 字符串；3: ref -> 2（别名，共享身份）
	g := &Graph{Nodes: []Node{
		{Kind: KindDict, Fields: map[string]int{"a": 2, "b": 3}},
		{Kind: KindString, Str: "unused-unreachable"},
		{Kind: KindString, Str: "v"},
		{Kind: KindRef, Ref: 2},
	}}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, g)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	defer res.Release()
	if res.Root.Fields["a"] != res.Root.Fields["b"] {
		t.Fatal("ref alias did not preserve target identity")
	}
}

func TestDecodeCapInCycleLeaseOnce(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register(tenantA, []string{"read"})
	// 同一能力藏在环内且多次出现：0 -> 1 -> 2 -> 0，节点 1、2 均为该能力。
	g := &Graph{Nodes: []Node{
		{Kind: KindArray, Items: []int{1, 2}},
		{Kind: KindCap, Token: tok},
		{Kind: KindArray, Items: []int{0, 1}},
	}}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, g)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// 重复引用只计一次租约。
	if got := reg.ActiveLeases(tok); got != 1 {
		t.Fatalf("ActiveLeases = %d, want 1", got)
	}
	// 环内多处引用共享同一能力句柄。
	c1 := res.Root.Items[0].Cap
	c2 := res.Root.Items[1].Items[1].Cap
	if c1 == nil || c1 != c2 {
		t.Fatal("capability handle not shared across cycle")
	}
	if !c1.Allows("read") || c1.Allows("write") || c1.Tenant() != tenantA {
		t.Fatalf("bad capability metadata: %+v", c1)
	}
	res.Release()
	res.Release() // 幂等
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases after release = %d, want 0", got)
	}
}

func TestDecodeCrossTenant(t *testing.T) {
	reg := NewRegistry()
	tokMine := reg.Register(tenantA, []string{"read"})
	tokOther := reg.Register("tenant-b", []string{"read"})

	res, err := (&Decoder{Reg: reg}).Decode(tenantA, capGraph(tokMine, tokOther))
	if !errors.Is(err, ErrCrossTenant) {
		t.Fatalf("Decode = %v, want ErrCrossTenant", err)
	}
	if res != nil {
		t.Fatal("partial result returned on failure")
	}
	// 原子性：本租户能力也不应留下租约。
	if got := reg.ActiveLeases(tokMine); got != 0 {
		t.Fatalf("ActiveLeases(mine) = %d, want 0", got)
	}
}

func TestDecodeDanglingToken(t *testing.T) {
	reg := NewRegistry()
	good := reg.Register(tenantA, []string{"read"})
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, capGraph(good, "forged-token"))
	if !errors.Is(err, ErrUnknownCapability) {
		t.Fatalf("Decode = %v, want ErrUnknownCapability", err)
	}
	if res != nil {
		t.Fatal("partial result returned on failure")
	}
	if got := reg.ActiveLeases(good); got != 0 {
		t.Fatalf("ActiveLeases(good) = %d, want 0", got)
	}
}

func TestDecodeDanglingRef(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register(tenantA, []string{"read"})
	g := &Graph{Nodes: []Node{
		{Kind: KindArray, Items: []int{1, 7}}, // 7 越界
		{Kind: KindCap, Token: tok},
	}}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, g)
	if !errors.Is(err, ErrBadReference) {
		t.Fatalf("Decode = %v, want ErrBadReference", err)
	}
	if res != nil {
		t.Fatal("partial result returned on failure")
	}
	// 整图验证先于租约：结构非法时不得持有任何租约。
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases = %d, want 0", got)
	}
}

func TestDecodeTooManyNodes(t *testing.T) {
	reg := NewRegistry()
	nodes := make([]Node, DefaultMaxNodes+1)
	for i := range nodes {
		nodes[i] = Node{Kind: KindString, Str: "s"}
	}
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, &Graph{Nodes: nodes}); !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("Decode = %v, want ErrTooManyNodes", err)
	}
}

func TestDecodeDeepCycle(t *testing.T) {
	reg := NewRegistry()
	// 1024 个数组节点首尾相接成深环。
	nodes := make([]Node, DefaultMaxNodes)
	for i := range nodes {
		nodes[i] = Node{Kind: KindArray, Items: []int{(i + 1) % len(nodes)}}
	}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, &Graph{Nodes: nodes})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	defer res.Release()
	cur := res.Root
	for i := 0; i < DefaultMaxNodes; i++ {
		cur = cur.Items[0]
	}
	if cur != res.Root {
		t.Fatal("deep cycle did not return to root identity")
	}
}

func TestDecodePureRefCycleRejected(t *testing.T) {
	reg := NewRegistry()
	g := &Graph{Nodes: []Node{
		{Kind: KindRef, Ref: 1},
		{Kind: KindRef, Ref: 0},
	}}
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, g); !errors.Is(err, ErrRefCycle) {
		t.Fatalf("Decode = %v, want ErrRefCycle", err)
	}
}

func TestDecodeStaleGeneration(t *testing.T) {
	reg := NewRegistry()
	old := reg.Register(tenantA, []string{"read"})
	cur, _ := reg.Reissue(old)

	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, capGraph(old)); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Decode old token = %v, want ErrStaleGeneration", err)
	}
	res, err := (&Decoder{Reg: reg}).Decode(tenantA, capGraph(cur))
	if err != nil {
		t.Fatalf("Decode current token: %v", err)
	}
	if got := res.Root.Cap.Generation(); got != 2 {
		t.Fatalf("Generation = %d, want 2", got)
	}
	res.Release()
}

// 可控调度：撤销在“整图验证通过、原子租约之前”完成，
// 解码必须失败且不产生任何租约与部分对象。
func TestRaceRevokeBeforeAcquire(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register(tenantA, []string{"read"})

	entered := make(chan struct{})
	proceed := make(chan struct{})
	d := &Decoder{Reg: reg, beforeAcquire: func() {
		close(entered)
		<-proceed
	}}

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := d.Decode(tenantA, capGraph(tok))
		done <- outcome{res, err}
	}()

	<-entered // 解码已完成整图验证，正停在租约获取前
	reg.Revoke(tok)
	close(proceed)

	out := <-done
	if !errors.Is(out.err, ErrRevoked) {
		t.Fatalf("Decode = %v, want ErrRevoked", out.err)
	}
	if out.res != nil {
		t.Fatal("partial result returned after losing race to revoke")
	}
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases = %d, want 0", got)
	}
}

// 可控调度：解码先取得租约，撤销随后到达；
// 已有租约可完成，撤销返回后新解码必须失败。
func TestRaceAcquireBeforeRevoke(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register(tenantA, []string{"read"})

	entered := make(chan struct{})
	proceed := make(chan struct{})
	d := &Decoder{Reg: reg, beforeAcquire: func() {
		close(entered)
		<-proceed
	}}

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := d.Decode(tenantA, capGraph(tok))
		done <- outcome{res, err}
	}()

	<-entered
	close(proceed) // 放行，让租约先落地
	out := <-done
	if out.err != nil {
		t.Fatalf("Decode: %v", out.err)
	}

	reg.Revoke(tok)
	// 已有租约可完成：撤销不清除在途租约。
	if got := reg.ActiveLeases(tok); got != 1 {
		t.Fatalf("ActiveLeases after revoke = %d, want 1", got)
	}
	// 撤销返回后不能取得新租约。
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, capGraph(tok)); !errors.Is(err, ErrRevoked) {
		t.Fatalf("Decode after revoke = %v, want ErrRevoked", err)
	}
	out.res.Release()
	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("ActiveLeases after release = %d, want 0", got)
	}
}

// 压力冒烟：并发解码与撤销交错，验证无数据竞争、无租约泄漏。
func TestConcurrentDecodeAndRevoke(t *testing.T) {
	reg := NewRegistry()
	tok := reg.Register(tenantA, []string{"read"})
	g := capGraph(tok)
	d := &Decoder{Reg: reg}

	var successes, failures atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res, err := d.Decode(tenantA, g)
				if err != nil {
					failures.Add(1)
					continue
				}
				successes.Add(1)
				res.Release()
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	reg.Revoke(tok)
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := reg.ActiveLeases(tok); got != 0 {
		t.Fatalf("leaked leases: %d", got)
	}
	if successes.Load() == 0 || failures.Load() == 0 {
		t.Fatalf("want both outcomes, ok=%d fail=%d", successes.Load(), failures.Load())
	}
}

func TestDecodeEmptyAndNilGraph(t *testing.T) {
	reg := NewRegistry()
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, nil); !errors.Is(err, ErrEmptyGraph) {
		t.Fatalf("nil graph = %v, want ErrEmptyGraph", err)
	}
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, &Graph{}); !errors.Is(err, ErrEmptyGraph) {
		t.Fatalf("empty graph = %v, want ErrEmptyGraph", err)
	}
}

func TestDecodeUnknownKind(t *testing.T) {
	reg := NewRegistry()
	g := &Graph{Nodes: []Node{{Kind: NodeKind(99)}}}
	if _, err := (&Decoder{Reg: reg}).Decode(tenantA, g); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("Decode = %v, want ErrUnknownKind", err)
	}
}

func ExampleDecoder_Decode() {
	reg := NewRegistry()
	tok := reg.Register("tenant-a", []string{"read"})
	g := &Graph{Nodes: []Node{
		{Kind: KindDict, Fields: map[string]int{"cap": 1, "name": 2}},
		{Kind: KindCap, Token: tok},
		{Kind: KindString, Str: "demo"},
	}}
	res, err := (&Decoder{Reg: reg}).Decode("tenant-a", g)
	if err != nil {
		panic(err)
	}
	defer res.Release()
	fmt.Println(res.Root.Fields["name"].Str, res.Root.Fields["cap"].Cap.Allows("read"))
	// Output: demo true
}
