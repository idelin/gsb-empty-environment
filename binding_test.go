package netsec

import (
	"net/netip"
	"testing"
	"time"
)

func mustPool(t *testing.T, base string, bits int) *PrefixPool {
	t.Helper()
	p, err := NewPrefixPool(netip.MustParsePrefix(base), bits)
	if err != nil {
		t.Fatalf("NewPrefixPool: %v", err)
	}
	return p
}

func newTestDelegator(t *testing.T, lease time.Duration) *Delegator {
	t.Helper()
	return NewDelegator(mustPool(t, "2001:db8:100::/48", 64), lease)
}

func solicit(t *testing.T, d *Delegator, duid string, iaid uint32, hint string, src string) *Reply {
	t.Helper()
	ia := IAPDRequest{IAID: iaid}
	if hint != "" {
		ia.Prefix = netip.MustParsePrefix(hint)
		ia.HasPrefix = true
	}
	rep, err := d.HandleMessage(&Message{
		Type:       MsgSolicit,
		ClientDUID: []byte(duid),
		IAPDs:      []IAPDRequest{ia},
		SourceAddr: netip.MustParseAddr(src),
	})
	if err != nil {
		t.Fatalf("HandleMessage(Solicit): %v", err)
	}
	return rep
}

// 提示冲突：他人已占用的提示不得采纳，应从池中另分前缀。
func TestHintConflict(t *testing.T) {
	d := newTestDelegator(t, time.Hour)

	repA := solicit(t, d, "duid-a", 1, "2001:db8:100:1::/64", "fe80::a")
	if repA.Results[0].Status != StatusSuccess {
		t.Fatalf("A status = %v", repA.Results[0].Status)
	}
	prefixA := repA.Results[0].Prefix
	if prefixA != netip.MustParsePrefix("2001:db8:100:1::/64") {
		t.Fatalf("A hint not honored: %v", prefixA)
	}
	// 回复确认前路由不得安装。
	if d.Routes.Len() != 0 {
		t.Fatalf("route installed before Commit")
	}
	repA.Commit()
	if r, ok := d.Routes.Lookup(prefixA); !ok || r.NextHop != netip.MustParseAddr("fe80::a") {
		t.Fatalf("A route missing after Commit: %v %v", r, ok)
	}

	repB := solicit(t, d, "duid-b", 1, "2001:db8:100:1::/64", "fe80::b")
	prefixB := repB.Results[0].Prefix
	if repB.Results[0].Status != StatusSuccess {
		t.Fatalf("B status = %v", repB.Results[0].Status)
	}
	if prefixB == prefixA {
		t.Fatalf("conflicting hint adopted: %v", prefixB)
	}
	repB.Commit()

	// 池外提示同样不得采纳。
	repC := solicit(t, d, "duid-c", 1, "2001:db8:999::/64", "fe80::c")
	prefixC := repC.Results[0].Prefix
	if prefixC == netip.MustParsePrefix("2001:db8:999::/64") {
		t.Fatalf("out-of-pool hint adopted: %v", prefixC)
	}
	repC.Commit()

	if d.Routes.Len() != 3 {
		t.Fatalf("routes = %d, want 3", d.Routes.Len())
	}
	if r, ok := d.Routes.Lookup(prefixA); !ok || r.NextHop != netip.MustParseAddr("fe80::a") {
		t.Fatalf("A route clobbered: %v %v", r, ok)
	}
}

// 他人 Release 与前缀不符的 Release 都必须返回 NoBinding，路由不动。
func TestForeignRelease(t *testing.T) {
	d := newTestDelegator(t, time.Hour)

	repA := solicit(t, d, "duid-a", 1, "2001:db8:100:1::/64", "fe80::a")
	repA.Commit()
	prefixA := repA.Results[0].Prefix

	release := func(duid string, prefix netip.Prefix) *Reply {
		t.Helper()
		rep, err := d.HandleMessage(&Message{
			Type:       MsgRelease,
			ClientDUID: []byte(duid),
			IAPDs:      []IAPDRequest{{IAID: 1, Prefix: prefix, HasPrefix: true}},
			SourceAddr: netip.MustParseAddr("fe80::dead"),
		})
		if err != nil {
			t.Fatalf("HandleMessage(Release): %v", err)
		}
		return rep
	}

	// 攻击者用自己的 DUID 释放他人前缀。
	rep := release("duid-evil", prefixA)
	if rep.Results[0].Status != StatusNoBinding {
		t.Fatalf("foreign release status = %v, want NoBinding", rep.Results[0].Status)
	}
	rep.Commit()
	if _, ok := d.Routes.Lookup(prefixA); !ok {
		t.Fatalf("A route withdrawn by foreign release")
	}

	// 本人 DUID 但前缀与绑定不一致。
	rep = release("duid-a", netip.MustParsePrefix("2001:db8:100:2::/64"))
	if rep.Results[0].Status != StatusNoBinding {
		t.Fatalf("mismatched release status = %v, want NoBinding", rep.Results[0].Status)
	}
	rep.Commit()
	if _, ok := d.Routes.Lookup(prefixA); !ok {
		t.Fatalf("A route withdrawn by mismatched release")
	}

	// 伪造 Renew 索要他人前缀同样 NoBinding。
	ren, err := d.HandleMessage(&Message{
		Type:       MsgRenew,
		ClientDUID: []byte("duid-evil"),
		IAPDs:      []IAPDRequest{{IAID: 1, Prefix: prefixA, HasPrefix: true}},
		SourceAddr: netip.MustParseAddr("fe80::dead"),
	})
	if err != nil {
		t.Fatalf("HandleMessage(Renew): %v", err)
	}
	if ren.Results[0].Status != StatusNoBinding {
		t.Fatalf("forged renew status = %v, want NoBinding", ren.Results[0].Status)
	}

	// 合法 Release 成功，确认后路由撤销。
	rep = release("duid-a", prefixA)
	if rep.Results[0].Status != StatusSuccess {
		t.Fatalf("legit release status = %v", rep.Results[0].Status)
	}
	if _, ok := d.Routes.Lookup(prefixA); !ok {
		t.Fatalf("route withdrawn before Commit")
	}
	rep.Commit()
	if _, ok := d.Routes.Lookup(prefixA); ok {
		t.Fatalf("route not withdrawn after release Commit")
	}
	if _, ok := d.Lookup(BindingKey{DUID: "duid-a", IAID: 1}); ok {
		t.Fatalf("binding survives release")
	}
}

// 中继链深度超过 32 必须拒绝；合法链按最内层 peer 装路由。
func TestRelayDepthLimit(t *testing.T) {
	d := newTestDelegator(t, time.Hour)
	d.LinkNets = []netip.Prefix{netip.MustParsePrefix("2001:db8:200::/64")}

	chain := func(n int, link string) []RelayHop {
		hops := make([]RelayHop, n)
		for i := range hops {
			hops[i] = RelayHop{PeerAddress: netip.MustParseAddr("fe80::1")}
		}
		if link != "" {
			hops[n-1].LinkAddress = netip.MustParseAddr(link)
		}
		return hops
	}
	msg := func(hops []RelayHop) *Message {
		return &Message{
			Type:       MsgSolicit,
			ClientDUID: []byte("duid-r"),
			IAPDs:      []IAPDRequest{{IAID: 7}},
			Relay:      hops,
		}
	}

	if _, err := d.HandleMessage(msg(chain(33, "2001:db8:200::1"))); err != ErrRelayTooDeep {
		t.Fatalf("33 hops err = %v, want ErrRelayTooDeep", err)
	}
	if d.Routes.Len() != 0 {
		t.Fatalf("route installed for oversized chain")
	}

	// 最内层 link-address 缺失或不在接入链路内都要拒绝。
	if _, err := d.HandleMessage(msg(chain(2, ""))); err != ErrRelayLinkAddress {
		t.Fatalf("missing inner link err = %v", err)
	}
	if _, err := d.HandleMessage(msg(chain(2, "2001:db8:999::1"))); err != ErrRelayLinkAddress {
		t.Fatalf("foreign link err = %v", err)
	}

	rep, err := d.HandleMessage(msg(chain(MaxRelayDepth, "2001:db8:200::1")))
	if err != nil {
		t.Fatalf("32 hops: %v", err)
	}
	rep.Commit()
	r, ok := d.Routes.Lookup(rep.Results[0].Prefix)
	if !ok || r.NextHop != netip.MustParseAddr("fe80::1") {
		t.Fatalf("relayed route = %+v %v", r, ok)
	}
}

// 租约到期后绑定清除、路由撤销，Renew 返回 NoBinding。
func TestLeaseExpiry(t *testing.T) {
	d := newTestDelegator(t, time.Hour)
	now := time.Now()
	d.Now = func() time.Time { return now }

	rep := solicit(t, d, "duid-a", 1, "2001:db8:100:1::/64", "fe80::a")
	rep.Commit()
	prefix := rep.Results[0].Prefix
	if d.Routes.Len() != 1 {
		t.Fatalf("routes = %d, want 1", d.Routes.Len())
	}

	// 租约内 Renew 成功并续期。
	now = now.Add(30 * time.Minute)
	ren, err := d.HandleMessage(&Message{
		Type:       MsgRenew,
		ClientDUID: []byte("duid-a"),
		IAPDs:      []IAPDRequest{{IAID: 1, Prefix: prefix, HasPrefix: true}},
		SourceAddr: netip.MustParseAddr("fe80::a"),
	})
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if ren.Results[0].Status != StatusSuccess {
		t.Fatalf("renew status = %v", ren.Results[0].Status)
	}
	ren.Commit()

	// 越过续期后的租约，到期清除。
	now = now.Add(time.Hour + time.Minute)
	if n := d.ReapExpired(); n != 1 {
		t.Fatalf("reaped = %d, want 1", n)
	}
	if _, ok := d.Routes.Lookup(prefix); ok {
		t.Fatalf("expired route not withdrawn")
	}
	if _, ok := d.Lookup(BindingKey{DUID: "duid-a", IAID: 1}); ok {
		t.Fatalf("expired binding survives")
	}

	// 到期后 Renew 返回 NoBinding。
	ren, err = d.HandleMessage(&Message{
		Type:       MsgRenew,
		ClientDUID: []byte("duid-a"),
		IAPDs:      []IAPDRequest{{IAID: 1, Prefix: prefix, HasPrefix: true}},
		SourceAddr: netip.MustParseAddr("fe80::a"),
	})
	if err != nil {
		t.Fatalf("renew after expiry: %v", err)
	}
	if ren.Results[0].Status != StatusNoBinding {
		t.Fatalf("renew after expiry status = %v, want NoBinding", ren.Results[0].Status)
	}

	// 前缀已回池，可被他人重新委派。
	repB := solicit(t, d, "duid-b", 1, prefix.String(), "fe80::b")
	if repB.Results[0].Prefix != prefix {
		t.Fatalf("expired prefix not reused: %v", repB.Results[0].Prefix)
	}
}
