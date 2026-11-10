package main

import (
	"strings"
	"testing"
	"time"
)

const baseInput = `{
  "query_time": "2026-11-10T08:00:00Z",
  "user": {"name": "alice", "delegation_disallowed": false},
  "tickets": [
    {"id": "TGT", "subject": "alice", "service": "krbtgt/EXAMPLE.ORG", "kind": "tgt",
     "forwardable": true, "not_before": "2026-11-10T06:00:00Z", "expires_at": "2026-11-10T16:00:00Z"},
    {"id": "WEB", "subject": "alice", "service": "HTTP/web", "kind": "tgs",
     "forwardable": true, "not_before": "2026-11-10T07:00:00Z", "expires_at": "2026-11-10T12:00:00Z"},
    {"id": "PRINT", "subject": "alice", "service": "print/print", "kind": "tgs",
     "forwardable": false, "not_before": "2026-11-10T07:00:00Z", "expires_at": "2026-11-10T12:00:00Z"},
    {"id": "OLD", "subject": "alice", "service": "db/olddb", "kind": "tgs",
     "forwardable": true, "not_before": "2026-11-10T05:00:00Z", "expires_at": "2026-11-10T07:30:00Z"},
    {"id": "BOB", "subject": "bob", "service": "HTTP/web", "kind": "tgs",
     "forwardable": true, "not_before": "2026-11-10T07:00:00Z", "expires_at": "2026-11-10T12:00:00Z"}
  ],
  "services": {
    "krbtgt/EXAMPLE.ORG": {"mode": "constrained", "delegate_to": []},
    "HTTP/web": {"mode": "constrained", "delegate_to": ["api/api", "backup/bak"]},
    "api/api": {"mode": "constrained", "delegate_to": ["HTTP/web", "files/fs"]},
    "files/fs": {"mode": "constrained", "delegate_to": ["api/api"]},
    "print/print": {"mode": "constrained", "delegate_to": ["files/fs"]},
    "backup/bak": {"mode": "unconstrained", "delegate_to": []}
  },
  "resource_allow": {
    "api/api": ["HTTP/web"],
    "files/fs": ["api/api", "backup/bak"]
  }
}`

func mustRun(t *testing.T, raw, policy string) *Result {
	t.Helper()
	in, err := ParseInput([]byte(raw))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	res, err := Run(in, policy)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func findReach(t *testing.T, pr PolicyResult, service string) Reach {
	t.Helper()
	for _, r := range pr.Reachable {
		if r.Service == service {
			return r
		}
	}
	t.Fatalf("服务 %s 不可达: %+v", service, pr.Reachable)
	return Reach{}
}

func assertNotReachable(t *testing.T, pr PolicyResult, service string) {
	t.Helper()
	for _, r := range pr.Reachable {
		if r.Service == service {
			t.Fatalf("服务 %s 不应可达，链: %v", service, r.Chain)
		}
	}
}

func hasBlockReason(pr PolicyResult, reason string) bool {
	for _, b := range pr.Blocked {
		if b.Reason == reason {
			return true
		}
	}
	return false
}

// 跨服务循环：web<->api<->files 构成环，BFS 必须终止且循环被记录。
func TestCycleIsSuppressed(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	pr := res.Policies["constrained"]

	api := findReach(t, pr, "api/api")
	if api.Hops != 2 || len(api.Chain) != 3 {
		t.Fatalf("api 最短链异常: %+v", api)
	}
	files := findReach(t, pr, "files/fs")
	if files.Hops != 3 {
		t.Fatalf("files 应在第 3 跳抵达，实际 hops=%d", files.Hops)
	}
	foundCycle := false
	for _, c := range pr.Cycles {
		if c.From == "api/api" && c.To == "HTTP/web" && c.Reason == "cycle_suppressed" {
			foundCycle = true
		}
	}
	if !foundCycle {
		t.Fatalf("未记录 api -> web 的循环抑制: %+v", pr.Cycles)
	}
}

// 最短票据链：抵达同一服务取跳数最少的路径，合成票据与身份链等长。
func TestShortestChainAndTickets(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	pr := res.Policies["constrained"]
	web := findReach(t, pr, "HTTP/web")
	if web.Hops != 1 || len(web.Tickets) != 1 || web.Tickets[0] != "WEB" {
		t.Fatalf("web 直达链异常: %+v", web)
	}
	files := findReach(t, pr, "files/fs")
	if len(files.Chain) != files.Hops+1 || len(files.Tickets) != files.Hops {
		t.Fatalf("链长与票据数不一致: %+v", files)
	}
	for _, ticket := range files.Tickets[1:] {
		if !strings.HasPrefix(ticket, "synth:") {
			t.Fatalf("委派跳应为合成票据: %s", ticket)
		}
	}
}

// 过期证据票：过期根票据被忽略，不会成为委派起点。
func TestExpiredEvidenceTicket(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	var ignored []string
	for _, it := range res.IgnoredTickets {
		ignored = append(ignored, it.ID+":"+it.Reason)
	}
	want := "OLD:expired"
	found := false
	for _, item := range ignored {
		if item == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("未忽略过期票据 OLD: %v", ignored)
	}
	pr := res.Policies["constrained"]
	assertNotReachable(t, pr, "db/olddb")
}

// 禁止委派标志：用户被标记后，所有委派跳被拒绝，只剩直达服务。
func TestUserDelegationDisallowed(t *testing.T) {
	raw := strings.Replace(baseInput,
		`"delegation_disallowed": false`,
		`"delegation_disallowed": true`, 1)
	res := mustRun(t, raw, "constrained")
	pr := res.Policies["constrained"]
	assertNotReachable(t, pr, "api/api")
	assertNotReachable(t, pr, "files/fs")
	findReach(t, pr, "HTTP/web")
	if !hasBlockReason(pr, "user_delegation_disallowed") {
		t.Fatalf("缺少 user_delegation_disallowed 阻断记录: %+v", pr.Blocked)
	}
}

// 不可转发票据：print 直达可达，但不能继续委派。
func TestNonForwardableTicket(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	pr := res.Policies["constrained"]
	print := findReach(t, pr, "print/print")
	if print.Hops != 1 || len(print.Tickets) != 1 || print.Tickets[0] != "PRINT" {
		t.Fatalf("print 最短链应为不可转发直达票: %+v", print)
	}
	blockedWithPrint := 0
	for _, b := range pr.Blocked {
		if b.From == "print/print" && b.Ticket == "PRINT" && b.Reason == "evidence_not_forwardable" {
			blockedWithPrint++
		}
	}
	if blockedWithPrint == 0 {
		t.Fatal("不可转发直达票 PRINT 的委派尝试应被 evidence_not_forwardable 阻断")
	}
}

// 两套策略差异：resource 策略只看资源端允许名单。
func TestPolicyDivergence(t *testing.T) {
	res := mustRun(t, baseInput, "both")
	c := res.Policies["constrained"]
	r := res.Policies["resource"]

	// 服务端名单允许 web -> backup，且 backup 为非约束模式，constrained 下可达。
	findReach(t, c, "backup/bak")
	// resource 策略下 web 不在任何资源的允许名单的反向关系中，backup 不可达。
	assertNotReachable(t, r, "backup/bak")
	// resource 策略允许 api 访问 files；api 本身由 web 的资源名单放行。
	findReach(t, r, "api/api")
	findReach(t, r, "files/fs")
	if !hasBlockReason(r, "not_in_resource_allow_list") {
		t.Fatalf("resource 策略缺少名单阻断记录")
	}
}

// 非约束服务：一旦抵达 backup，可二次委派到任意已定义服务。
func TestUnconstrainedSecondHop(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	pr := res.Policies["constrained"]
	findReach(t, pr, "backup/bak")
	// backup 非约束，可借道再次抵达 print（print 本身只有不可转发直达票）。
	print := findReach(t, pr, "print/print")
	if print.Hops != 1 {
		t.Fatalf("print 最短链应保持 1 跳直达: %+v", print)
	}
}

// 中段证据过期：BFS 节点携带的证据窗口已越过查询时刻时，后续跳全部阻断。
func TestMidChainEvidenceExpiry(t *testing.T) {
	in := mustParse(baseInput)
	at := mustTime(t, "2026-11-10T08:00:00Z")
	expired := &bfsState{
		service: "HTTP/web",
		fwd:     true,
		chain:   []string{"alice", "HTTP/web"},
		tickets: []string{"WEB"},
		nbf:     mustTime(t, "2026-11-10T05:00:00Z"),
		exp:     mustTime(t, "2026-11-10T07:30:00Z"),
	}
	pr := in.bfs("constrained", at, map[string]*bfsState{"HTTP/web": expired}, in.allTargets())
	if !hasBlockReason(pr, "evidence_expired") {
		t.Fatalf("缺少 evidence_expired 阻断记录: %+v", pr.Blocked)
	}
	assertNotReachable(t, pr, "api/api")
}

// 不可转发证据：非 fwd 节点展开时必须被门控拦截。
func TestNonForwardableBFSGate(t *testing.T) {
	in := mustParse(baseInput)
	at := mustTime(t, "2026-11-10T08:00:00Z")
	stale := &bfsState{
		service: "print/print",
		fwd:     false,
		chain:   []string{"alice", "print/print"},
		tickets: []string{"PRINT"},
		nbf:     mustTime(t, "2026-11-10T07:00:00Z"),
		exp:     mustTime(t, "2026-11-10T12:00:00Z"),
	}
	pr := in.bfs("constrained", at, map[string]*bfsState{"print/print": stale}, in.allTargets())
	if !hasBlockReason(pr, "evidence_not_forwardable") {
		t.Fatalf("缺少 evidence_not_forwardable 阻断记录: %+v", pr.Blocked)
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("时间解析失败 %s: %v", value, err)
	}
	return parsed
}

// 主体过滤：其他用户的票据不得参与本次复核。
func TestSubjectMismatchIgnored(t *testing.T) {
	res := mustRun(t, baseInput, "constrained")
	for _, it := range res.IgnoredTickets {
		if it.ID == "BOB" && it.Reason == "subject_mismatch" {
			return
		}
	}
	t.Fatalf("BOB 票据未按主体过滤: %+v", res.IgnoredTickets)
}

// 非法策略与非法服务模式要报错。
func TestInvalidInputs(t *testing.T) {
	if _, err := Run(mustParse(baseInput), "bogus"); err == nil {
		t.Fatal("非法策略应报错")
	}
	bad := strings.Replace(baseInput, `"mode": "constrained"`, `"mode": "weird"`, 1)
	if _, err := ParseInput([]byte(bad)); err == nil {
		t.Fatal("非法服务 mode 应报错")
	}
}

func mustParse(raw string) *Input {
	in, err := ParseInput([]byte(raw))
	if err != nil {
		panic(err)
	}
	return in
}
