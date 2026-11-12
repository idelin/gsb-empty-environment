package netsec

import "testing"

func mustPerms(t *testing.T, s string) Perm {
	t.Helper()
	p, err := ParsePerms(s)
	if err != nil {
		t.Fatalf("ParsePerms(%q): %v", s, err)
	}
	return p
}

func TestGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/home/*", "/home/a", true},
		{"/home/*", "/home/a/b", false},   // * 不跨 /
		{"/home/**", "/home/a/b/c", true}, // ** 跨 /
		{"/home/**", "/home", false},      // ** 需匹配 / 之后的内容
		{"/proc/*/mem", "/proc/self/mem", true},
		{"/proc/*/mem", "/proc/1/mem", true},
		{"/proc/*/mem", "/proc/self/status", false},
		{"**", "/any/depth/at/all", true},
		{"/etc/passwd", "/etc/passwd", true},
		{"/etc/passwd", "/etc/shadow", false},
	}
	for _, c := range cases {
		if got := matchGlob(c.pattern, c.path); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

// 场景一：deny 覆盖通配 allow。
func TestDenyOverridesWildcardAllow(t *testing.T) {
	p := &Profile{Name: "web", Rules: []Rule{
		{Pattern: "/home/**", Perms: mustPerms(t, "rw")},
		{Pattern: "/home/*/secret", Perms: mustPerms(t, "rw"), Deny: true},
	}}
	if !p.Decide("/home/app/data", PermRead) {
		t.Error("通配 allow 应放行 /home/app/data 读")
	}
	if !p.Decide("/home/app/data", PermWrite) {
		t.Error("通配 allow 应放行 /home/app/data 写")
	}
	if p.Decide("/home/app/secret", PermRead) {
		t.Error("deny 规则应覆盖通配 allow，拒绝 /home/app/secret 读")
	}
	if p.Decide("/home/app/secret", PermWrite) {
		t.Error("deny 规则应覆盖通配 allow，拒绝 /home/app/secret 写")
	}
}

// allow 取并集：分散在多条规则里的权限应合并生效。
func TestAllowUnion(t *testing.T) {
	p := &Profile{Name: "svc", Rules: []Rule{
		{Pattern: "/var/lib/svc", Perms: mustPerms(t, "r")},
		{Pattern: "/var/lib/svc", Perms: mustPerms(t, "w")},
	}}
	if !p.Decide("/var/lib/svc", PermRead) || !p.Decide("/var/lib/svc", PermWrite) {
		t.Error("allow 并集应同时放行 r 与 w")
	}
	if p.Decide("/var/lib/svc", PermExec) {
		t.Error("未授予的 x 不应放行")
	}
}

// 场景二：受限配置中的 ux 规则被报告，且解析结果为无约束。
func TestUnconfinedExecRisk(t *testing.T) {
	pol := NewPolicy(&Profile{Name: "app", Rules: []Rule{
		{Pattern: "/usr/bin/*", Perms: mustPerms(t, "x"), Exec: ExecUnconfined},
	}})
	risks := pol.Audit()
	if len(risks) != 1 || risks[0].Kind != RiskUnconfinedExec {
		t.Fatalf("Audit() = %v, 期望一条 unconfined-exec 风险", risks)
	}
	res, err := pol.ResolveExec("app", "/usr/bin/python3")
	if err != nil {
		t.Fatalf("ResolveExec: %v", err)
	}
	if !res.Unconfined {
		t.Error("ux 执行应脱离所有配置")
	}
}

// 场景三：ix 继承当前配置，写权限随继承延续到子进程。
func TestInheritExecKeepsWritePerm(t *testing.T) {
	pol := NewPolicy(&Profile{Name: "worker", Rules: []Rule{
		{Pattern: "/var/tmp/**", Perms: mustPerms(t, "rw")},
		{Pattern: "/bin/**", Perms: mustPerms(t, "x"), Exec: ExecInherit},
	}})
	res, err := pol.ResolveExec("worker", "/bin/sh")
	if err != nil {
		t.Fatalf("ResolveExec: %v", err)
	}
	if res.Profile != "worker" || res.Unconfined {
		t.Fatalf("ix 应继承当前配置 worker，得到 %+v", res)
	}
	child := pol.Profiles[res.Profile]
	if !child.Decide("/var/tmp/evil.sh", PermWrite) {
		t.Error("ix 继承后子进程仍持有当前配置的写权限")
	}
}

// 场景四：/proc/self/mem 写权限被判定放行并报告风险；deny 可封堵。
func TestProcSelfMemWrite(t *testing.T) {
	risky := &Profile{Name: "debugger", Rules: []Rule{
		{Pattern: "/proc/*/mem", Perms: mustPerms(t, "rw")},
	}}
	if !risky.Decide("/proc/self/mem", PermWrite) {
		t.Fatal("/proc/*/mem 应命中 /proc/self/mem")
	}
	risks := AuditProfile(risky)
	if len(risks) != 1 || risks[0].Kind != RiskProcMemWrite {
		t.Fatalf("AuditProfile() = %v, 期望一条 proc-mem-write 风险", risks)
	}

	hardened := &Profile{Name: "debugger", Rules: []Rule{
		{Pattern: "/proc/*/mem", Perms: mustPerms(t, "rw")},
		{Pattern: "/proc/*/mem", Perms: mustPerms(t, "w"), Deny: true},
	}}
	if hardened.Decide("/proc/self/mem", PermWrite) {
		t.Error("deny 应封堵 /proc/self/mem 写")
	}
	if !hardened.Decide("/proc/self/mem", PermRead) {
		t.Error("deny 仅含 w，读仍应放行")
	}
	if got := AuditProfile(hardened); len(got) != 0 {
		t.Errorf("deny 规则不应触发风险报告，得到 %v", got)
	}
}

// 对 ** 授予 w 权限必须报告。
func TestWriteAllRisk(t *testing.T) {
	pol := NewPolicy(&Profile{Name: "rootish", Rules: []Rule{
		{Pattern: "**", Perms: mustPerms(t, "rw")},
	}})
	risks := pol.Audit()
	var kinds = map[RiskKind]bool{}
	for _, r := range risks {
		kinds[r.Kind] = true
	}
	if !kinds[RiskWriteAll] {
		t.Errorf("对 ** 的 w 应报告 write-all，得到 %v", risks)
	}
	if !kinds[RiskProcMemWrite] {
		t.Errorf("** 同时覆盖 /proc/self/mem，应报告 proc-mem-write，得到 %v", risks)
	}
}

// px 切换到命名配置，缺失则失败；Px 额外清理环境。
func TestProfileExecTransitions(t *testing.T) {
	pol := NewPolicy(
		&Profile{Name: "launcher", Rules: []Rule{
			{Pattern: "/usr/bin/safe", Perms: mustPerms(t, "x"), Exec: ExecProfile, Target: "safe"},
			{Pattern: "/usr/bin/clean", Perms: mustPerms(t, "x"), Exec: ExecProfileClean, Target: "safe"},
			{Pattern: "/usr/bin/ghost", Perms: mustPerms(t, "x"), Exec: ExecProfile, Target: "ghost"},
		}},
		&Profile{Name: "safe", Rules: []Rule{
			{Pattern: "/tmp/**", Perms: mustPerms(t, "rw")},
		}},
	)
	res, err := pol.ResolveExec("launcher", "/usr/bin/safe")
	if err != nil {
		t.Fatalf("ResolveExec: %v", err)
	}
	if res.Profile != "safe" || res.CleanEnv {
		t.Errorf("px 应切换到 safe 且不清理环境，得到 %+v", res)
	}
	res, err = pol.ResolveExec("launcher", "/usr/bin/clean")
	if err != nil {
		t.Fatalf("ResolveExec: %v", err)
	}
	if res.Profile != "safe" || !res.CleanEnv {
		t.Errorf("Px 应切换到 safe 且清理环境，得到 %+v", res)
	}
	if _, err := pol.ResolveExec("launcher", "/usr/bin/ghost"); err == nil {
		t.Error("px 目标配置缺失应执行失败")
	}
}

// deny 的执行规则应使执行失败。
func TestDenyExecFails(t *testing.T) {
	pol := NewPolicy(&Profile{Name: "jail", Rules: []Rule{
		{Pattern: "/bin/**", Perms: mustPerms(t, "x"), Exec: ExecInherit},
		{Pattern: "/bin/sh", Perms: mustPerms(t, "x"), Deny: true},
	}})
	if _, err := pol.ResolveExec("jail", "/bin/sh"); err == nil {
		t.Error("deny 命中的执行应失败")
	}
	if _, err := pol.ResolveExec("jail", "/bin/ls"); err != nil {
		t.Errorf("未命中 deny 的执行应成功: %v", err)
	}
}
