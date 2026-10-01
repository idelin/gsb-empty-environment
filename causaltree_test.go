package causaltree

import (
	"reflect"
	"testing"
)

// 辅助：在 doc 上顺序输入一段文本，返回产生的操作序列。
func typeText(d *Doc, text string) []Node {
	var ops []Node
	parent := d.LastID()
	for _, ch := range text {
		op := d.InsertAfter(parent, ch)
		ops = append(ops, op)
		parent = op.ID
	}
	return ops
}

// 辅助：把操作逐一应用到 doc（模拟网络广播同步）。
func syncOps(d *Doc, ops []Node) {
	for _, op := range ops {
		d.Apply(op)
	}
}

// 验收边界：两个客户端在 "hello" 后并发输入 '!' 和 '?'，
// 两字符共享同一父节点，同步后双端逐字一致且为 "hello!?"。
func TestConcurrentInsertConverges(t *testing.T) {
	alice := New("A")
	bob := New("B")

	// Alice 输入 "hello"，同步给 Bob，两端建立共同基础。
	base := typeText(alice, "hello")
	syncOps(bob, base)
	if alice.String() != "hello" || bob.String() != "hello" {
		t.Fatalf("base mismatch: alice=%q bob=%q", alice.String(), bob.String())
	}

	// 两端基于同一父节点（'o'）并发插入，互不可见。
	parent := alice.LastID()
	opBang := alice.InsertAfter(parent, '!') // Lamport 6 @ A
	opQ := bob.InsertAfter(parent, '?')      // Lamport 6 @ B（并发，时间戳相同）

	if opBang.Parent != opQ.Parent {
		t.Fatalf("expected shared parent, got %v vs %v", opBang.Parent, opQ.Parent)
	}
	if opBang.ID.Lamport != opQ.ID.Lamport {
		t.Fatalf("expected concurrent ops to share Lamport, got %d vs %d",
			opBang.ID.Lamport, opQ.ID.Lamport)
	}

	// 同步前两端视图不同。
	if alice.String() != "hello!" || bob.String() != "hello?" {
		t.Fatalf("pre-sync: alice=%q bob=%q", alice.String(), bob.String())
	}

	// 交换操作（故意以相反顺序投递，验证与到达顺序无关）。
	bob.Apply(opBang)
	alice.Apply(opQ)

	// 因果树按 (Lamport, ClientID) 确定性排定为 "hello!?"，双端逐字一致。
	const want = "hello!?"
	if got := alice.String(); got != want {
		t.Errorf("alice: got %q, want %q", got, want)
	}
	if got := bob.String(); got != want {
		t.Errorf("bob: got %q, want %q", got, want)
	}
	if !reflect.DeepEqual(alice.Chars(), bob.Chars()) {
		t.Errorf("char sequences diverged:\nalice=%v\nbob=%v", alice.Chars(), bob.Chars())
	}
}

// 时间戳不同时，纯按 Lamport 排序（无需客户端 ID 决胜）。
func TestLamportOrdersSiblings(t *testing.T) {
	a := New("A")
	b := New("B")
	base := typeText(a, "ab")
	syncOps(b, base)

	// B 先插（Lamport 较小），A 后插（经同步后 Lamport 更大）。
	op1 := b.InsertAfter(b.LastID(), 'x')
	a.Apply(op1)
	op2 := a.InsertAfter(a.LastID(), 'y')
	b.Apply(op2)

	if got := a.String(); got != "abxy" {
		t.Errorf("a: got %q, want %q", got, "abxy")
	}
	if got := b.String(); got != "abxy" {
		t.Errorf("b: got %q, want %q", got, "abxy")
	}
}

// 操作以任意顺序、重复投递，文档仍收敛（幂等 + 交换律）。
func TestOutOfOrderAndDuplicateDelivery(t *testing.T) {
	a := New("A")
	b := New("B")
	ops := typeText(a, "crdt")
	// 乱序 + 重复投递给 B。
	syncOps(b, []Node{ops[2], ops[0], ops[3], ops[1], ops[2], ops[0]})
	if got := b.String(); got != "crdt" {
		t.Errorf("got %q, want %q", got, "crdt")
	}
	if !reflect.DeepEqual(a.Chars(), b.Chars()) {
		t.Errorf("diverged: a=%v b=%v", a.Chars(), b.Chars())
	}
}

// 多方并发在同一位置插入，所有端收敛到同一确定性序列。
func TestMultiClientSamePointConvergence(t *testing.T) {
	clients := []*Doc{New("A"), New("B"), New("C")}
	base := typeText(clients[0], "hi")
	for _, d := range clients[1:] {
		syncOps(d, base)
	}
	parent := clients[0].LastID()
	var ops []Node
	for i, d := range clients {
		ops = append(ops, d.InsertAfter(parent, rune('1'+i))) // '1','2','3' 并发
	}
	// 以打乱顺序广播给所有端。
	for _, d := range clients {
		syncOps(d, []Node{ops[2], ops[0], ops[1]})
	}
	for _, d := range clients {
		if got := d.String(); got != "hi123" {
			t.Errorf("client: got %q, want %q", got, "hi123")
		}
	}
}
