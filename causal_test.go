package taskpkg

import (
	"crypto/ed25519"
	"errors"
	"testing"
)

type testNode struct {
	id   NodeID
	keys map[uint64]ed25519.PrivateKey
}

func newNode(reg *KeyRegistry, id NodeID, epochs ...uint64) *testNode {
	n := &testNode{id: id, keys: make(map[uint64]ed25519.PrivateKey)}
	for _, ep := range epochs {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			panic(err)
		}
		n.keys[ep] = priv
		reg.Register(id, ep, pub)
	}
	return n
}

// emit 构造并签名一个事件。prev 为同节点同代次上一事件（可为 nil 表示 Seq 1）。
func (n *testNode) emit(t *testing.T, epoch, seq uint64, prev *Event, refs []*Event, kind Kind, payload string, clock int64) *Event {
	t.Helper()
	e := &Event{
		Node:      n.id,
		Epoch:     epoch,
		Seq:       seq,
		Kind:      kind,
		Payload:   []byte(payload),
		WallClock: clock,
	}
	if prev != nil {
		e.Prev = prev.Hash()
	}
	for _, r := range refs {
		e.Refs = append(e.Refs, r.Hash())
	}
	priv, ok := n.keys[epoch]
	if !ok {
		t.Fatalf("node %s has no key for epoch %d", n.id, epoch)
	}
	e.Sign(priv)
	return e
}

func mustAdd(t *testing.T, s *Store, e *Event) Hash {
	t.Helper()
	h, err := s.Add(e)
	if err != nil {
		t.Fatalf("Add(%s seq %d): %v", e.Node, e.Seq, err)
	}
	return h
}

func mustCompare(t *testing.T, s *Store, op, revoke Hash) Verdict {
	t.Helper()
	v, err := s.Compare(op, revoke)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	return v
}

// 伪时间：操作自报墙钟早于撤权，但操作引用了撤权事件，
// 因果判定必须为 After，墙钟仅显示。
func TestFakeClockIgnored(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	b := newNode(reg, "B", 1)
	s := NewStore(reg)

	revoke := a.emit(t, 1, 1, nil, nil, KindRevocation, "revoke B", 100)
	mustAdd(t, s, revoke)
	// B 的操作引用了撤权事件，却把墙钟伪报为 1（远早于 100）。
	op := b.emit(t, 1, 1, nil, []*Event{revoke}, KindOperation, "unauthorized write", 1)
	mustAdd(t, s, op)

	if v := mustCompare(t, s, op.Hash(), revoke.Hash()); v != After {
		t.Fatalf("op references revoke but verdict = %v, want after", v)
	}

	// 反向伪时间：撤权引用了操作，却伪报更早的墙钟。
	reg2 := NewKeyRegistry()
	c := newNode(reg2, "C", 1)
	d := newNode(reg2, "D", 1)
	s2 := NewStore(reg2)
	op2 := c.emit(t, 1, 1, nil, nil, KindOperation, "legit write", 500)
	mustAdd(t, s2, op2)
	revoke2 := d.emit(t, 1, 1, nil, []*Event{op2}, KindRevocation, "revoke C", 10)
	mustAdd(t, s2, revoke2)
	if v := mustCompare(t, s2, op2.Hash(), revoke2.Hash()); v != Before {
		t.Fatalf("revoke references op but verdict = %v, want before", v)
	}
}

// 跨代拼接：代次滚动换钥后，新代次首事件引用旧代次末事件，
// 因果链跨代延续；用旧代次密钥签新代次事件必须被拒绝。
func TestCrossEpochSplicing(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1, 2)
	b := newNode(reg, "B", 1)
	s := NewStore(reg)

	a1 := a.emit(t, 1, 1, nil, nil, KindCheckpoint, "A epoch1 begin", 10)
	revoke := a.emit(t, 1, 2, a1, nil, KindRevocation, "revoke B", 20)
	mustAdd(t, s, a1)
	mustAdd(t, s, revoke)

	// A 滚到代次 2，首事件引用旧代次末事件（撤权），维持跨代因果链。
	a2gen := a.emit(t, 2, 1, nil, []*Event{revoke}, KindCheckpoint, "A epoch2 genesis", 30)
	mustAdd(t, s, a2gen)

	// B 在代次 2 的 A 事件之后发起操作，引用 A 的代次 2 首事件。
	op := b.emit(t, 1, 1, nil, []*Event{a2gen}, KindOperation, "write after rotation", 40)
	mustAdd(t, s, op)

	if v := mustCompare(t, s, op.Hash(), revoke.Hash()); v != After {
		t.Fatalf("cross-epoch chain: verdict = %v, want after", v)
	}

	// 拼接攻击 1：用代次 1 的密钥签代次 2 的事件。
	forged := &Event{Node: "A", Epoch: 2, Seq: 9, Kind: KindOperation, Payload: []byte("spliced")}
	forged.Sign(a.keys[1])
	if _, err := s.Add(forged); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("old-epoch key on new-epoch event: err = %v, want ErrBadSignature", err)
	}

	// 拼接攻击 2：未注册的代次。
	ghost := &Event{Node: "A", Epoch: 3, Seq: 1, Kind: KindOperation, Payload: []byte("ghost")}
	ghost.Sign(a.keys[2])
	if _, err := s.Add(ghost); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unregistered epoch: err = %v, want ErrUnknownKey", err)
	}
}

// 双签：同节点同代次同序号两份异内容事件，签名均有效，
// 必须输出双签证据。
func TestDoubleSignEvidence(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	s := NewStore(reg)

	e1 := a.emit(t, 1, 1, nil, nil, KindOperation, "transfer 10", 10)
	e2 := a.emit(t, 1, 1, nil, nil, KindOperation, "transfer 999", 10)
	mustAdd(t, s, e1)

	_, err := s.Add(e2)
	var ev *DoubleSignEvidence
	if !errors.As(err, &ev) {
		t.Fatalf("err = %v, want DoubleSignEvidence", err)
	}
	if ev.Node != "A" || ev.Epoch != 1 || ev.Seq != 1 {
		t.Fatalf("evidence identity = %s/%d/%d", ev.Node, ev.Epoch, ev.Seq)
	}
	pub, _ := reg.lookup("A", 1)
	if !ev.A.VerifySignature(pub) || !ev.B.VerifySignature(pub) {
		t.Fatal("both forks in evidence must carry valid signatures")
	}
	if ev.A.Hash() == ev.B.Hash() {
		t.Fatal("evidence forks must differ")
	}
}

// 缺失引用：闭包不完备时返回 Incomplete；补齐后判定生效。
func TestMissingReferenceIncomplete(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	b := newNode(reg, "B", 1)
	s := NewStore(reg)

	revoke := a.emit(t, 1, 1, nil, nil, KindRevocation, "revoke B", 10)
	op := b.emit(t, 1, 1, nil, []*Event{revoke}, KindOperation, "write", 20)
	// 先只入库操作，撤权事件缺失。
	mustAdd(t, s, op)

	if v := mustCompare(t, s, op.Hash(), revoke.Hash()); v != Incomplete {
		t.Fatalf("missing revoke event: verdict = %v, want incomplete", v)
	}
	miss, err := s.Missing(op.Hash())
	if err != nil || len(miss) != 1 || miss[0] != revoke.Hash() {
		t.Fatalf("Missing = %v, %v", miss, err)
	}

	mustAdd(t, s, revoke)
	if v := mustCompare(t, s, op.Hash(), revoke.Hash()); v != After {
		t.Fatalf("after backfill: verdict = %v, want after", v)
	}
}

// 环：依赖图出现环时必须报错而非误判或死循环。
// 哈希链下环无法真实构造，此处白盒注入邻接环验证检测器。
func TestCycleDetected(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	s := NewStore(reg)

	e1 := a.emit(t, 1, 1, nil, nil, KindOperation, "x", 1)
	e2 := a.emit(t, 1, 2, e1, nil, KindRevocation, "y", 2)
	// 绕过 Add 直接注入互指环。
	h1, h2 := e1.Hash(), e2.Hash()
	e1.Refs = []Hash{h2}
	e2.Prev = h1
	s.events[h1] = e1
	s.events[h2] = e2

	if _, err := s.Compare(h1, h2); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
}

// 严格序号：Prev 必须指向同节点同代次的 Seq-1。
func TestStrictSequence(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	s := NewStore(reg)

	e1 := a.emit(t, 1, 1, nil, nil, KindOperation, "one", 1)
	mustAdd(t, s, e1)
	// 跳号：Seq 3 的 Prev 指向 Seq 1。
	bad := a.emit(t, 1, 3, e1, nil, KindOperation, "three", 3)
	if _, err := s.Add(bad); !errors.Is(err, ErrBadPrev) {
		t.Fatalf("err = %v, want ErrBadPrev", err)
	}
	// Seq 1 携带非零 Prev。
	badGen := &Event{Node: "A", Epoch: 1, Seq: 1, Prev: e1.Hash(), Kind: KindOperation}
	badGen.Sign(a.keys[1])
	if _, err := s.Add(badGen); !errors.Is(err, ErrBadSeq) {
		t.Fatalf("err = %v, want ErrBadSeq", err)
	}
}

// 小图全拓扑序核对：对枚举出的每一个拓扑序验证
// Compare 的判定与“所有拓扑序中的先后”一致。
func TestAllTopologicalOrders(t *testing.T) {
	reg := NewKeyRegistry()
	a := newNode(reg, "A", 1)
	b := newNode(reg, "B", 1)
	s := NewStore(reg)

	// 图：
	//   a1 -> a2(revoke)      b1 -> b2(op, refs a2)
	//   c 链与二者无序（用第三个节点制造并发对）
	c := newNode(reg, "C", 1)
	a1 := a.emit(t, 1, 1, nil, nil, KindCheckpoint, "a1", 1)
	revoke := a.emit(t, 1, 2, a1, nil, KindRevocation, "revoke", 2)
	b1 := b.emit(t, 1, 1, nil, nil, KindCheckpoint, "b1", 3)
	op := b.emit(t, 1, 2, b1, []*Event{revoke}, KindOperation, "op", 4)
	c1 := c.emit(t, 1, 1, nil, nil, KindOperation, "c1 concurrent", 5)
	for _, e := range []*Event{a1, revoke, b1, op, c1} {
		mustAdd(t, s, e)
	}
	all := []*Event{a1, revoke, b1, op, c1}

	// 枚举全部拓扑序。
	orders := topoOrders(all)
	if len(orders) == 0 {
		t.Fatal("no topological orders found")
	}

	check := func(t *testing.T, x, y *Event) {
		t.Helper()
		xFirst, yFirst := false, false
		for _, ord := range orders {
			pos := make(map[Hash]int, len(ord))
			for i, h := range ord {
				pos[h] = i
			}
			if pos[x.Hash()] < pos[y.Hash()] {
				xFirst = true
			} else {
				yFirst = true
			}
		}
		want := Concurrent
		switch {
		case xFirst && !yFirst:
			want = Before // x 必然在 y 前
		case yFirst && !xFirst:
			want = After // x 必然在 y 后
		}
		got := mustCompare(t, s, x.Hash(), y.Hash())
		if got != want {
			t.Fatalf("Compare(%s,%s) = %v, want %v (over %d topo orders)",
				x.Payload, y.Payload, got, want, len(orders))
		}
	}

	// 操作必然在撤权后；c1 与二者无序；a1 必然在撤权前。
	check(t, op, revoke) // After
	check(t, c1, revoke) // Concurrent
	check(t, c1, op)     // Concurrent
	check(t, a1, revoke) // Before
	check(t, b1, op)     // Before
	check(t, revoke, op) // Before（对称方向）
}

// topoOrders 枚举事件集的全部拓扑序（依赖必须排在引用者之前）。
func topoOrders(events []*Event) [][]Hash {
	idx := make(map[Hash]*Event, len(events))
	for _, e := range events {
		idx[e.Hash()] = e
	}
	indegree := make(map[Hash]int, len(events))
	dependents := make(map[Hash][]Hash, len(events))
	for _, e := range events {
		h := e.Hash()
		indegree[h] += 0
		for _, d := range e.deps() {
			if _, ok := idx[d]; !ok {
				continue
			}
			indegree[h]++
			dependents[d] = append(dependents[d], h)
		}
	}
	var avail []Hash
	for h, d := range indegree {
		if d == 0 {
			avail = append(avail, h)
		}
	}
	var out [][]Hash
	var path []Hash
	var bt func(avail []Hash)
	bt = func(avail []Hash) {
		if len(avail) == 0 {
			cp := make([]Hash, len(path))
			copy(cp, path)
			out = append(out, cp)
			return
		}
		for i, h := range avail {
			next := make([]Hash, 0, len(avail)+len(dependents[h]))
			next = append(next, avail[:i]...)
			next = append(next, avail[i+1:]...)
			for _, dep := range dependents[h] {
				indegree[dep]--
				if indegree[dep] == 0 {
					next = append(next, dep)
				}
			}
			path = append(path, h)
			bt(next)
			path = path[:len(path)-1]
			for _, dep := range dependents[h] {
				indegree[dep]++
			}
		}
	}
	bt(avail)
	return out
}
