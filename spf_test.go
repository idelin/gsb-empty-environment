package netsec

import (
	"context"
	"net/netip"
	"sync"
	"testing"
)

// scriptedDNS 是脚本化的 DNS 接口：按域名返回预置记录或错误，
// 并统计每个域名的查询次数，不访问真实域名。
type scriptedDNS struct {
	mu      sync.Mutex
	records map[string][]string
	errs    map[string]error
	calls   map[string]int
}

func newScriptedDNS() *scriptedDNS {
	return &scriptedDNS{
		records: make(map[string][]string),
		errs:    make(map[string]error),
		calls:   make(map[string]int),
	}
}

func (d *scriptedDNS) LookupTXT(_ context.Context, domain string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls[domain]++
	if err, ok := d.errs[domain]; ok {
		return nil, err
	}
	return d.records[domain], nil
}

func (d *scriptedDNS) queries(domain string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[domain]
}

func mustIP(t *testing.T, s string) netip.Addr {
	t.Helper()
	ip, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("bad test ip %q: %v", s, err)
	}
	return ip
}

func check(t *testing.T, ev *Evaluator, ip, domain string) (Result, error) {
	t.Helper()
	return ev.CheckHost(context.Background(), mustIP(t, ip), domain)
}

func expectResult(t *testing.T, ev *Evaluator, ip, domain string, want Result) {
	t.Helper()
	got, err := check(t, ev, ip, domain)
	if err != nil {
		t.Fatalf("CheckHost(%s, %s) error: %v", ip, domain, err)
	}
	if got != want {
		t.Fatalf("CheckHost(%s, %s) = %s, want %s", ip, domain, got, want)
	}
}

func expectErrorKind(t *testing.T, ev *Evaluator, ip, domain string, want ErrorKind) {
	t.Helper()
	_, err := check(t, ev, ip, domain)
	if err == nil {
		t.Fatalf("CheckHost(%s, %s) expected error kind %v, got nil", ip, domain, want)
	}
	pe, ok := err.(*Error)
	if !ok {
		t.Fatalf("CheckHost(%s, %s) error type %T, want *Error", ip, domain, err)
	}
	if pe.Kind != want {
		t.Fatalf("CheckHost(%s, %s) error kind = %v, want %v", ip, domain, pe.Kind, want)
	}
}

func TestBasicMechanismsAndQualifiers(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["example.com"] = []string{"v=spf1 ip4:192.0.2.0/24 -ip4:198.51.100.9 ~ip6:2001:db8::/32 ?all"}
	ev := &Evaluator{Resolver: dns}

	expectResult(t, ev, "192.0.2.7", "example.com", Pass)
	expectResult(t, ev, "198.51.100.9", "example.com", Fail)
	expectResult(t, ev, "2001:db8::1", "example.com", SoftFail)
	expectResult(t, ev, "203.0.113.1", "example.com", Neutral)
}

func TestEmptyResults(t *testing.T) {
	dns := newScriptedDNS()
	// 完全无 TXT 记录。
	expectResult(t, &Evaluator{Resolver: dns}, "192.0.2.1", "absent.example", None)
	// 有 TXT 记录但没有策略记录。
	dns.records["other.example"] = []string{"google-site-verification=xyz", "v=spf10 include:a"}
	expectResult(t, &Evaluator{Resolver: dns}, "192.0.2.1", "other.example", None)
	// 同一域发布多条策略记录是永久错误。
	dns.records["dup.example"] = []string{"v=spf1 all", "v=spf1 -all"}
	expectErrorKind(t, &Evaluator{Resolver: dns}, "192.0.2.1", "dup.example", PermError)
}

func TestIncludeSemantics(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 include:b.example -all"}
	dns.records["b.example"] = []string{"v=spf1 ip4:192.0.2.0/24"}
	ev := &Evaluator{Resolver: dns}

	// 子策略 pass -> include 匹配，应用外层前缀。
	expectResult(t, ev, "192.0.2.5", "a.example", Pass)
	// 子策略未 pass（默认 neutral）-> include 不匹配，继续到 -all。
	expectResult(t, ev, "203.0.113.9", "a.example", Fail)
}

func TestIncludeTempErrorPropagates(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 include:b.example -all"}
	dns.errs["b.example"] = &DNSError{Temporary: true, Message: "SERVFAIL"}
	expectErrorKind(t, &Evaluator{Resolver: dns}, "192.0.2.1", "a.example", TempError)

	// 顶层域自身查询临时失败同样传播。
	dns2 := newScriptedDNS()
	dns2.errs["top.example"] = &DNSError{Temporary: true, Message: "timeout"}
	expectErrorKind(t, &Evaluator{Resolver: dns2}, "192.0.2.1", "top.example", TempError)
}

func TestIncludeOfEmptyDomainIsPermError(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 include:empty.example -all"}
	expectErrorKind(t, &Evaluator{Resolver: dns}, "192.0.2.1", "a.example", PermError)
}

func TestRedirectOnlyWhenUnmatched(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 ip4:192.0.2.0/24 redirect=b.example"}
	dns.records["b.example"] = []string{"v=spf1 -all"}
	ev := &Evaluator{Resolver: dns}

	// 机制匹配时不执行 redirect，b.example 不应被查询。
	expectResult(t, ev, "192.0.2.1", "a.example", Pass)
	if n := dns.queries("b.example"); n != 0 {
		t.Fatalf("redirect target queried %d times despite earlier match", n)
	}
	// 未匹配时才走 redirect。
	expectResult(t, ev, "203.0.113.1", "a.example", Fail)
	if n := dns.queries("b.example"); n != 1 {
		t.Fatalf("redirect target queried %d times, want 1", n)
	}
}

func TestPolicyLoopsArePermError(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["self.example"] = []string{"v=spf1 include:self.example"}
	dns.records["a.example"] = []string{"v=spf1 include:b.example"}
	dns.records["b.example"] = []string{"v=spf1 include:a.example"}
	dns.records["r1.example"] = []string{"v=spf1 redirect=r2.example"}
	dns.records["r2.example"] = []string{"v=spf1 redirect=r1.example"}
	ev := &Evaluator{Resolver: dns}

	expectErrorKind(t, ev, "192.0.2.1", "self.example", PermError)
	expectErrorKind(t, ev, "192.0.2.1", "a.example", PermError)
	expectErrorKind(t, ev, "192.0.2.1", "r1.example", PermError)
}

func TestRepeatedQueriesUseCache(t *testing.T) {
	dns := newScriptedDNS()
	// 同一子域被 include 两次，且经 redirect 再次引用。
	dns.records["a.example"] = []string{"v=spf1 include:b.example include:b.example redirect=b.example"}
	dns.records["b.example"] = []string{"v=spf1 ip4:203.0.113.0/24"}
	ev := &Evaluator{Resolver: dns}

	expectResult(t, ev, "203.0.113.8", "a.example", Pass)
	if n := dns.queries("b.example"); n != 1 {
		t.Fatalf("b.example queried %d times, want 1 (cache)", n)
	}
	// 未匹配路径上 redirect 复用同一会话缓存，仍只查一次。
	dns = newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 include:b.example redirect=b.example"}
	dns.records["b.example"] = []string{"v=spf1 ip4:203.0.113.0/24"}
	ev = &Evaluator{Resolver: dns}
	expectResult(t, ev, "192.0.2.8", "a.example", Neutral)
	if n := dns.queries("b.example"); n != 1 {
		t.Fatalf("b.example queried %d times after redirect, want 1 (cache)", n)
	}
	if n := dns.queries("a.example"); n != 1 {
		t.Fatalf("a.example queried %d times, want 1", n)
	}
}

func TestBudgetExactlyExhausted(t *testing.T) {
	build := func() *scriptedDNS {
		dns := newScriptedDNS()
		dns.records["a.example"] = []string{"v=spf1 include:b.example -all"}
		dns.records["b.example"] = []string{"v=spf1 ip4:192.0.2.0/24"}
		return dns
	}

	// 预算恰好等于所需查询数：评估成功。
	dns := build()
	ev := &Evaluator{Resolver: dns, MaxLookups: 2}
	expectResult(t, ev, "192.0.2.3", "a.example", Pass)
	if n := dns.queries("b.example"); n != 1 {
		t.Fatalf("b.example queried %d times, want 1", n)
	}

	// 预算少一个：第 2 次查询触发永久错误。
	dns = build()
	ev = &Evaluator{Resolver: dns, MaxLookups: 1}
	expectErrorKind(t, ev, "192.0.2.3", "a.example", PermError)
	if n := dns.queries("b.example"); n != 0 {
		t.Fatalf("b.example queried %d times after budget exhausted, want 0", n)
	}

	// 缓存命中不消耗预算：预算 2 也能完成含重复引用的评估。
	dns = newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 include:b.example include:b.example -all"}
	dns.records["b.example"] = []string{"v=spf1 ip4:192.0.2.0/24"}
	ev = &Evaluator{Resolver: dns, MaxLookups: 2}
	expectResult(t, ev, "192.0.2.3", "a.example", Pass)
}

func TestRedirectToEmptyDomainIsPermError(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["a.example"] = []string{"v=spf1 ip4:192.0.2.0/24 redirect=empty.example"}
	expectErrorKind(t, &Evaluator{Resolver: dns}, "203.0.113.1", "a.example", PermError)
}

func TestMalformedRecords(t *testing.T) {
	dns := newScriptedDNS()
	dns.records["badmech.example"] = []string{"v=spf1 exists:%{i} -all"}
	dns.records["badip.example"] = []string{"v=spf1 ip4:2001:db8::1"}
	dns.records["badbits.example"] = []string{"v=spf1 ip4:192.0.2.0/33"}
	ev := &Evaluator{Resolver: dns}

	expectErrorKind(t, ev, "192.0.2.1", "badmech.example", PermError)
	expectErrorKind(t, ev, "192.0.2.1", "badip.example", PermError)
	expectErrorKind(t, ev, "192.0.2.1", "badbits.example", PermError)
}
