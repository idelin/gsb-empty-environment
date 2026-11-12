package netsec

import (
	"errors"
	"testing"
	"time"
)

const testTTL = 300 * time.Second

func cnameRR(name, target, zone string, ttl time.Duration) RR {
	return RR{Name: name, Type: TypeCNAME, TTL: ttl, Target: target}
}

func aRR(name, data string, ttl time.Duration) RR {
	return RR{Name: name, Type: TypeA, TTL: ttl, Data: data}
}

func soaRR(zone string, ttl time.Duration) RR {
	return RR{Name: zone, Type: TypeSOA, TTL: ttl, Data: "soa"}
}

func newTestComposer(r Resolver) *Composer {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	return NewComposer(r, WithClock(func() time.Time { return now }))
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 跨区别名：起始名在 example.com，经一层 CNAME 落到 cdn.example.net 的 A 记录。
func TestCrossZoneAlias(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("www.example.com", TypeA, &Message{
		QueryName: "www.example.com",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer:    []RR{cnameRR("www.example.com", "edge.cdn.example.net", "example.com", testTTL)},
	})
	r.SetReply("edge.cdn.example.net", TypeA, &Message{
		QueryName: "edge.cdn.example.net",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "cdn.example.net",
		Answer:    []RR{aRR("edge.cdn.example.net", "203.0.113.7", 60*time.Second)},
	})

	c := newTestComposer(r)
	res, err := c.Resolve("WWW.Example.COM.", TypeA)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	wantPath := []string{"www.example.com", "edge.cdn.example.net"}
	if got := res.TrustPath(); !equalStrings(got, wantPath) {
		t.Fatalf("信任路径 = %v, 期望 %v", got, wantPath)
	}
	if res.TerminalName() != "edge.cdn.example.net" {
		t.Fatalf("末端名字错误: %s", res.TerminalName())
	}
	if len(res.Hops) != 2 {
		t.Fatalf("跳数 = %d, 期望 2", len(res.Hops))
	}
	if res.Hops[0].Zone != "example.com" || res.Hops[1].Zone != "cdn.example.net" {
		t.Fatalf("权威区记录错误: %+v", res.Hops)
	}
	for _, hop := range res.Hops {
		if hop.QueryType != TypeA {
			t.Fatalf("每跳查询类型都应为原始 A: %+v", hop)
		}
		if hop.ExpiresAt.IsZero() {
			t.Fatalf("每跳都应记录绝对失效时刻: %+v", hop)
		}
	}
	if res.MinTTL != 60*time.Second {
		t.Fatalf("最短有效期 = %v, 期望 60s", res.MinTTL)
	}
	if len(res.Records) != 1 || res.Records[0].Data != "203.0.113.7" {
		t.Fatalf("末端 A 记录错误: %+v", res.Records)
	}
	if res.Negative != nil {
		t.Fatal("肯定解析不应带否定结论")
	}
}

// 单条应答内携带完整 CNAME 链，也应逐跳记录。
func TestFullChainInOneMessage(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("a.example.com", TypeA, &Message{
		QueryName: "a.example.com",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer: []RR{
			cnameRR("a.example.com", "b.example.com", "example.com", 100*time.Second),
			cnameRR("b.example.com", "c.example.net", "example.net", 200*time.Second),
			aRR("c.example.net", "192.0.2.9", 50*time.Second),
		},
	})
	c := newTestComposer(r)
	res, err := c.Resolve("a.example.com", TypeA)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	want := []string{"a.example.com", "b.example.com", "c.example.net"}
	if got := res.TrustPath(); !equalStrings(got, want) {
		t.Fatalf("信任路径 = %v, 期望 %v", got, want)
	}
	if len(res.Hops) != 3 || res.MinTTL != 50*time.Second {
		t.Fatalf("跳数与最短 TTL 错误: %+v", res.Hops)
	}
}

// 同名不同类型：末端 A 为 NODATA，但 AAAA 存在；否定只覆盖 (末端, A)。
func TestSameNameDifferentTypes(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("alias.example.com", TypeA, &Message{
		QueryName: "alias.example.com",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer:    []RR{cnameRR("alias.example.com", "target.example.org", "example.com", testTTL)},
	})
	r.SetReply("target.example.org", TypeA, &Message{
		QueryName: "target.example.org",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.org",
		Authority: []RR{soaRR("example.org", 90*time.Second)},
	})
	r.SetReply("target.example.org", TypeAAAA, &Message{
		QueryName: "target.example.org",
		QueryType: TypeAAAA,
		Rcode:     RcodeNoError,
		Zone:      "example.org",
		Answer:    []RR{{Name: "target.example.org", Type: TypeAAAA, TTL: testTTL, Data: "2001:db8::7"}},
	})
	// 同一别名对 AAAA 查询同样成立（CNAME 与查询类型无关）。
	r.SetReply("alias.example.com", TypeAAAA, &Message{
		QueryName: "alias.example.com",
		QueryType: TypeAAAA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer:    []RR{cnameRR("alias.example.com", "target.example.org", "example.com", testTTL)},
	})

	c := newTestComposer(r)

	resA, err := c.Resolve("alias.example.com", TypeA)
	if err != nil {
		t.Fatalf("A 查询失败: %v", err)
	}
	if resA.Negative == nil || resA.Negative.NXDOMAIN {
		t.Fatalf("应为 NODATA 否定结论: %+v", resA.Negative)
	}
	if resA.Negative.Name != "target.example.org" || resA.Negative.Type != TypeA {
		t.Fatalf("否定结论作用域错误: %+v", resA.Negative)
	}
	if resA.Negative.Covers("alias.example.com", TypeA) {
		t.Fatal("NODATA 错误地覆盖了起始名字")
	}
	if resA.Negative.Covers("target.example.org", TypeAAAA) {
		t.Fatal("NODATA 错误地覆盖了末端名字的其它类型")
	}
	if !resA.Negative.Covers("target.example.org", TypeA) {
		t.Fatal("NODATA 应覆盖末端名字的同一类型")
	}

	resAAAA, err := c.Resolve("alias.example.com", TypeAAAA)
	if err != nil {
		t.Fatalf("AAAA 查询失败: %v", err)
	}
	if resAAAA.Negative != nil || len(resAAAA.Records) != 1 {
		t.Fatalf("AAAA 应独立得到肯定记录: %+v", resAAAA)
	}
}

// 途中超时：第二跳查询超时，返回部分信任路径，且不产生任何否定结论。
func TestMidChainTimeout(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("start.example.com", TypeA, &Message{
		QueryName: "start.example.com",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer:    []RR{cnameRR("start.example.com", "slow.example.net", "example.com", testTTL)},
	})
	r.SetTimeout("slow.example.net", TypeA)

	c := newTestComposer(r)
	res, err := c.Resolve("start.example.com", TypeA)
	if res != nil {
		t.Fatal("超时不应返回完整结果")
	}
	var incomplete *IncompleteChainError
	if !errors.As(err, &incomplete) {
		t.Fatalf("期望 IncompleteChainError，得到 %T: %v", err, err)
	}
	wantNames := []string{"start.example.com", "slow.example.net"}
	if got := incomplete.Partial.Names(); !equalStrings(got, wantNames) {
		t.Fatalf("部分路径 = %v, 期望 %v", got, wantNames)
	}
	if len(incomplete.Partial.Hops) != 1 || incomplete.Partial.Hops[0].Negative != nil {
		t.Fatalf("超时跳不得编造否定结论: %+v", incomplete.Partial.Hops)
	}
}

// 末端 NXDOMAIN：否定结论只覆盖被证明的末端及其下属，不覆盖起始名。
func TestTerminalNXDOMAINScope(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("www.example.com", TypeA, &Message{
		QueryName: "www.example.com",
		QueryType: TypeA,
		Rcode:     RcodeNoError,
		Zone:      "example.com",
		Answer:    []RR{cnameRR("www.example.com", "gone.example.org", "example.com", testTTL)},
	})
	r.SetReply("gone.example.org", TypeA, &Message{
		QueryName:   "gone.example.org",
		QueryType:   TypeA,
		Rcode:       RcodeNXDomain,
		Zone:        "example.org",
		NegativeTTL: 120 * time.Second,
		Authority:   []RR{soaRR("example.org", 120*time.Second)},
	})

	c := newTestComposer(r)
	res, err := c.Resolve("www.example.com", TypeA)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if res.Negative == nil || !res.Negative.NXDOMAIN {
		t.Fatalf("应得到末端 NXDOMAIN 结论: %+v", res.Negative)
	}
	if res.Negative.Name != "gone.example.org" || res.Negative.Type != TypeAny {
		t.Fatalf("NXDOMAIN 结论字段错误: %+v", res.Negative)
	}
	if res.Negative.Covers("www.example.com", TypeA) ||
		res.Negative.Covers("www.example.com", TypeAAAA) ||
		res.Negative.Covers("example.com", TypeAny) {
		t.Fatal("NXDOMAIN 错误地向上覆盖了起始名")
	}
	if !res.Negative.Covers("gone.example.org", TypeA) ||
		!res.Negative.Covers("sub.gone.example.org", TypeAAAA) {
		t.Fatal("NXDOMAIN 应覆盖末端本身及其下属名字的任意类型")
	}
	if res.Hops[len(res.Hops)-1].TTL != 120*time.Second {
		t.Fatal("否定跳应记录有效期")
	}
}

// 循环别名：链回到已访问名字时拒绝。
func TestCNAMELoop(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("a.example.com", TypeA, &Message{
		QueryName: "a.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.com",
		Answer: []RR{cnameRR("a.example.com", "b.example.com", "example.com", testTTL)},
	})
	r.SetReply("b.example.com", TypeA, &Message{
		QueryName: "b.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.com",
		Answer: []RR{cnameRR("b.example.com", "a.example.com", "example.com", testTTL)},
	})

	c := newTestComposer(r)
	_, err := c.Resolve("a.example.com", TypeA)
	var loopErr *LoopError
	if !errors.As(err, &loopErr) {
		t.Fatalf("期望 LoopError，得到 %T: %v", err, err)
	}
	if loopErr.Name != "a.example.com" {
		t.Fatalf("循环点错误: %s", loopErr.Name)
	}
}

// 跨查询的相互矛盾别名：同一名字在有效期内指向不同目标时拒绝，过期后允许更新。
func TestCrossCallContradiction(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("svc.example.com", TypeA, &Message{
		QueryName: "svc.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.com",
		Answer: []RR{cnameRR("svc.example.com", "target-one.example.net", "example.com", testTTL)},
	})
	r.SetReply("target-one.example.net", TypeA, &Message{
		QueryName: "target-one.example.net", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.net",
		Answer: []RR{aRR("target-one.example.net", "198.51.100.1", testTTL)},
	})

	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	c := NewComposer(r, WithClock(func() time.Time { return now }))

	if _, err := c.Resolve("svc.example.com", TypeA); err != nil {
		t.Fatalf("首次解析应成功: %v", err)
	}

	r.SetReply("svc.example.com", TypeA, &Message{
		QueryName: "svc.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.com",
		Answer: []RR{cnameRR("svc.example.com", "target-two.example.net", "example.com", testTTL)},
	})
	_, err := c.Resolve("svc.example.com", TypeA)
	var conflict *ContradictionError
	if !errors.As(err, &conflict) {
		t.Fatalf("期望 ContradictionError，得到 %T: %v", err, err)
	}

	now = now.Add(testTTL + time.Second)
	r.SetReply("target-two.example.net", TypeA, &Message{
		QueryName: "target-two.example.net", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.net",
		Answer: []RR{aRR("target-two.example.net", "198.51.100.2", testTTL)},
	})
	if _, err := c.Resolve("svc.example.com", TypeA); err != nil {
		t.Fatalf("TTL 过期后应允许新别名: %v", err)
	}
}

// 单条应答内的矛盾：一个名字给出两个不同 CNAME 目标。
func TestInMessageConflictingCNAME(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("bad.example.com", TypeA, &Message{
		QueryName: "bad.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone: "example.com",
		Answer: []RR{
			cnameRR("bad.example.com", "x.example.net", "example.com", testTTL),
			cnameRR("bad.example.com", "y.example.net", "example.com", testTTL),
		},
	})
	c := newTestComposer(r)
	_, err := c.Resolve("bad.example.com", TypeA)
	var conflict *ContradictionError
	if !errors.As(err, &conflict) {
		t.Fatalf("期望 ContradictionError，得到 %T: %v", err, err)
	}
}

// CNAME 与同类型数据共存违反别名规则，应拒绝。
func TestCNAMEWithTerminalData(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("mix.example.com", TypeA, &Message{
		QueryName: "mix.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone: "example.com",
		Answer: []RR{
			cnameRR("mix.example.com", "else.example.net", "example.com", testTTL),
			aRR("mix.example.com", "198.51.100.50", testTTL),
		},
	})
	c := newTestComposer(r)
	_, err := c.Resolve("mix.example.com", TypeA)
	var conflict *ContradictionError
	if !errors.As(err, &conflict) {
		t.Fatalf("期望 ContradictionError，得到 %T: %v", err, err)
	}
}

// SERVFAIL 不是可缓存否定，绝不能被当成末端不存在。
func TestServFailNotNegative(t *testing.T) {
	r := NewScriptedResolver()
	r.SetReply("www.example.com", TypeA, &Message{
		QueryName: "www.example.com", QueryType: TypeA, Rcode: RcodeNoError,
		Zone:   "example.com",
		Answer: []RR{cnameRR("www.example.com", "broken.example.net", "example.com", testTTL)},
	})
	r.SetReply("broken.example.net", TypeA, &Message{
		QueryName: "broken.example.net", QueryType: TypeA, Rcode: RcodeServFail,
		Zone: "example.net",
	})
	c := newTestComposer(r)
	res, err := c.Resolve("www.example.com", TypeA)
	if err == nil || res != nil {
		t.Fatalf("SERVFAIL 应报错且无结果: res=%v err=%v", res, err)
	}
}

func TestCanonicalAndSubDomain(t *testing.T) {
	if got := canonicalName("WWW.Example.COM."); got != "www.example.com" {
		t.Fatalf("规范化错误: %q", got)
	}
	if !isSubDomain("a.b.example", "b.example") {
		t.Fatal("下属域判断错误")
	}
	if isSubDomain("b.example", "b.example") || isSubDomain("notb.example", "b.example") {
		t.Fatal("相等或不同后缀不应判为下属域")
	}
}
