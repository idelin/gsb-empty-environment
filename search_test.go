package netsec

import "testing"

var testKey = []byte("host-secret-key")

// payment 构造一条支付消息（未签名）。
func payment(from, to, op string, amount int64) *Node {
	return MapOf(map[string]*Node{
		"header": MapOf(map[string]*Node{
			"from":  Str(from),
			"nonce": Str("n-" + from),
		}),
		"body": MapOf(map[string]*Node{
			"to":     Str(to),
			"op":     Str(op),
			"amount": Int(amount),
		}),
		"sig": MapOf(map[string]*Node{}),
	})
}

func signed(t *testing.T, msg *Node, covers ...string) *Node {
	t.Helper()
	if err := Sign(msg, testKey, "sig", covers); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return msg
}

func verifyHost(m *Node) bool { return Verify(m, testKey, "sig") }

func TestRecipientSwapOnPartialCoverage(t *testing.T) {
	original := signed(t, payment("alice", "bob", "pay", 10), "header", "body.op")
	observed := signed(t, payment("alice", "carol", "pay", 99), "header", "body.op")

	f, err := Search(original, []*Node{observed}, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if f == nil {
		t.Fatal("期望找到替换集，实际为空")
	}
	if len(f.Replacements) != 1 {
		t.Fatalf("期望最小替换集大小为 1，实际 %d (%s)", len(f.Replacements), f.Describe())
	}
	r := f.Replacements[0]
	if r.Kind != ModifyLeaf || r.Path != "body.to" {
		t.Fatalf("期望 modify body.to，实际 %s %s", r.Kind, r.Path)
	}
	if f.After.Recipient != "carol" || f.After.Operation != "pay" {
		t.Fatalf("授权目标异常: %+v", f.After)
	}

	rep, err := Replay(original, []*Node{observed}, f, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("重放核验未通过: %+v", rep)
	}
}

func TestSubtreeCopyAtCoveredAncestor(t *testing.T) {
	// 只覆盖 body.op：攻击者整体复制观察消息的 body（op 相同、to 不同）。
	original := signed(t, payment("alice", "bob", "pay", 10), "body.op")
	observed := signed(t, payment("alice", "carol", "pay", 10), "body.op")

	f, err := Search(original, []*Node{observed}, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if f == nil {
		t.Fatal("期望找到替换集，实际为空")
	}
	if len(f.Replacements) != 1 {
		t.Fatalf("期望最小替换集大小为 1，实际 %d (%s)", len(f.Replacements), f.Describe())
	}
	if f.After.Recipient != "carol" {
		t.Fatalf("接收方未被替换: %+v", f.After)
	}

	rep, err := Replay(original, []*Node{observed}, f, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("重放核验未通过: %+v", rep)
	}
}

func TestFullCoverageYieldsNoFinding(t *testing.T) {
	original := signed(t, payment("alice", "bob", "pay", 10), "header", "body")
	observed := signed(t, payment("alice", "carol", "pay", 10), "header", "body")

	f, err := Search(original, []*Node{observed}, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if f != nil {
		t.Fatalf("全覆盖时不应有替换集: %s", f.Describe())
	}
}

func TestOperationSwapNeedsCoveredIdentical(t *testing.T) {
	// op 未覆盖：攻击者把操作从 transfer 改为 observe 到的 admin。
	original := signed(t, payment("alice", "bob", "transfer", 10), "header", "body.to")
	observed := signed(t, payment("alice", "bob", "admin", 10), "header", "body.to")

	f, err := Search(original, []*Node{observed}, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if f == nil {
		t.Fatal("期望找到替换集，实际为空")
	}
	if f.After.Operation != "admin" {
		t.Fatalf("操作未被替换: %+v", f.After)
	}
	rep, err := Replay(original, []*Node{observed}, f, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("重放核验未通过: %+v", rep)
	}
}

func TestReplayRejectsDishonestFinding(t *testing.T) {
	original := signed(t, payment("alice", "bob", "pay", 10), "header", "body.op")
	observed := signed(t, payment("alice", "carol", "pay", 10), "header", "body.op")

	f, err := Search(original, []*Node{observed}, verifyHost, Options{})
	if err != nil || f == nil {
		t.Fatalf("Search: %v, finding=%v", err, f)
	}

	// 篡改 Forged：与替换清单不一致。
	bad := *f
	bad.Forged = f.Forged.Clone()
	if err := bad.Forged.Set("body.amount", Int(999)); err != nil {
		t.Fatal(err)
	}
	rep, err := Replay(original, []*Node{observed}, &bad, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rep.Consistent {
		t.Fatal("篡改后的 Forged 不应与替换清单一致")
	}
	if rep.OK() {
		t.Fatal("篡改后的 Finding 不应通过重放")
	}

	// 非法替换：修改了被覆盖的字段。
	illegal := &Finding{
		Replacements: []Replacement{{
			Path: "body.op", Kind: ModifyLeaf, Source: 0, Value: Str("admin"),
		}},
	}
	illegal.Forged, err = applyReplacements(original, illegal.Replacements)
	if err != nil {
		t.Fatal(err)
	}
	rep, err = Replay(original, []*Node{observed}, illegal, verifyHost, Options{})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if rep.Legal {
		t.Fatal("修改覆盖字段不应被判为合法")
	}
	if rep.SignatureOK {
		t.Fatal("修改覆盖字段不应通过验证谓词")
	}
}

func TestCandidateCap(t *testing.T) {
	original := signed(t, payment("alice", "bob", "pay", 10), "header")
	var obs []*Node
	for _, to := range []string{"c1", "c2", "c3", "c4", "c5", "c6", "c7", "c8", "c9"} {
		obs = append(obs, signed(t, payment("alice", to, "pay", 10), "header"))
	}
	opts := Options{}
	covered, err := CoveredPaths(original, "sig")
	if err != nil {
		t.Fatal(err)
	}
	cands := buildCandidates(original, obs, covered, opts.withDefaults())
	if len(cands) > 16 {
		t.Fatalf("候选子树超过 16: %d", len(cands))
	}
}
