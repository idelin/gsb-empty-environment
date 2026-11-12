package netsec

import (
	"encoding/json"
	"os"
	"testing"
)

const (
	uidAdmin    = uint32(1000)
	uidOperator = uint32(1001)
	uidReadonly = uint32(1002)
	uidUnknown  = uint32(4096)
	pidTest     = int32(31337)
)

func testPolicy() *Policy {
	return &Policy{
		Roles: map[string]*RolePolicy{
			RoleAdmin: {
				Name:        RoleAdmin,
				Admin:       true,
				AllowedDirs: []string{"/srv/vm/admin", "/srv/vm/shared"},
			},
			RoleOperator: {
				Name:        RoleOperator,
				AllowedDirs: []string{"/srv/vm/operator"},
			},
			RoleReadonly: {
				Name:      RoleReadonly,
				QueryOnly: true,
			},
		},
		UIDRoles: map[uint32]string{
			uidAdmin:    RoleAdmin,
			uidOperator: RoleOperator,
			uidReadonly: RoleReadonly,
		},
		RequireSafeSocketMode: true,
	}
}

func newTestAuthorizer(t *testing.T) *Authorizer {
	t.Helper()
	authorizer, err := NewAuthorizer(testPolicy())
	if err != nil {
		t.Fatalf("NewAuthorizer returned error: %v", err)
	}
	return authorizer
}

func peer(uid uint32) PeerCred {
	return PeerCred{UID: uid, GID: uid, PID: pidTest, Mode: os.FileMode(0o660)}
}

func qmp(execute string, args any, id any) json.RawMessage {
	request := map[string]any{"execute": execute}
	if args != nil {
		request["arguments"] = args
	}
	if id != nil {
		request["id"] = id
	}
	raw, _ := json.Marshal(request)
	return raw
}

func assertAllowed(t *testing.T, decision Decision, context string) {
	t.Helper()
	if !decision.Allowed {
		t.Errorf("%s: expected allow, got deny reason=%q detail=%q", context, decision.Reason, decision.Detail)
	}
}

func assertDenied(t *testing.T, decision Decision, wantReason, context string) {
	t.Helper()
	if decision.Allowed {
		t.Errorf("%s: expected deny (%s), got allow", context, wantReason)
		return
	}
	if decision.Reason != wantReason {
		t.Errorf("%s: expected reason %q, got %q (%q)", context, wantReason, decision.Reason, decision.Detail)
	}
}

func negotiate(t *testing.T, authorizer *Authorizer, uid uint32) {
	t.Helper()
	decision := authorizer.Authorize(peer(uid), qmp("qmp_capabilities", nil, "cap"))
	assertAllowed(t, decision, "qmp_capabilities negotiation")
}

// 场景 1：未完成 qmp_capabilities 协商时，除协商命令外一律拒绝。
func TestCapabilitiesNegotiationRequired(t *testing.T) {
	authorizer := newTestAuthorizer(t)

	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), qmp("query-status", nil, 1)),
		ReasonCapabilitiesFirst,
		"admin before negotiation")
	assertDenied(t,
		authorizer.Authorize(peer(uidOperator), qmp("device_add", map[string]any{"driver": "virtio-net-pci"}, 2)),
		ReasonCapabilitiesFirst,
		"operator before negotiation")

	negotiate(t, authorizer, uidAdmin)

	assertAllowed(t,
		authorizer.Authorize(peer(uidAdmin), qmp("query-status", nil, 3)),
		"admin after negotiation")

	// 重复协商必须拒绝。
	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), qmp("qmp_capabilities", nil, 4)),
		ReasonDuplicateCapab,
		"duplicate qmp_capabilities")

	// Reset 后需要重新协商。
	authorizer.Reset(peer(uidAdmin))
	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), qmp("query-status", nil, 5)),
		ReasonCapabilitiesFirst,
		"after Reset")
}

// 场景 2：越权角色——human-monitor-command 仅 admin；只读角色仅 query-。
func TestRoleAuthorization(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	negotiate(t, authorizer, uidAdmin)
	negotiate(t, authorizer, uidOperator)
	negotiate(t, authorizer, uidReadonly)

	hmp := qmp("human-monitor-command", map[string]any{"command-line": "info status"}, "h")
	assertAllowed(t, authorizer.Authorize(peer(uidAdmin), hmp), "admin uses HMP")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), hmp),
		ReasonAdminOnly, "operator blocked from HMP")
	assertDenied(t, authorizer.Authorize(peer(uidReadonly), hmp),
		ReasonAdminOnly, "readonly blocked from HMP")

	assertAllowed(t,
		authorizer.Authorize(peer(uidReadonly), qmp("query-block", nil, "q1")),
		"readonly query- allowed")
	assertDenied(t,
		authorizer.Authorize(peer(uidReadonly), qmp("device_add", map[string]any{"driver": "virtio-net-pci"}, "d1")),
		ReasonReadonlyForbidden,
		"readonly mutation denied")

	// operator 可执行普通变更命令。
	assertAllowed(t,
		authorizer.Authorize(peer(uidOperator), qmp("device_add", map[string]any{"driver": "virtio-net-pci"}, "d2")),
		"operator ordinary command")

	// 未登记 uid 的对端无法获得任何角色。
	assertDenied(t,
		authorizer.Authorize(peer(uidUnknown), qmp("qmp_capabilities", nil, "x")),
		ReasonUnknownPeer,
		"unmapped peer")

	// 全局可写套接字上的对端被整体拒绝。
	unsafePeer := PeerCred{UID: uidAdmin, GID: uidAdmin, PID: pidTest, Mode: os.FileMode(0o666)}
	assertDenied(t,
		authorizer.Authorize(unsafePeer, qmp("query-status", nil, "u")),
		ReasonUnsafeSocketMode,
		"world-writable socket")
}

// 场景 3：/dev、/proc 路径与登记目录限制。
func TestPathRestrictions(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	negotiate(t, authorizer, uidOperator)
	negotiate(t, authorizer, uidAdmin)

	// driver=host_device 指向 /dev，任何命令上都拒绝。
	hostDev := qmp("device_add", map[string]any{
		"driver": "host_device",
		"path":   "/dev/sda",
	}, "hd")
	assertDenied(t, authorizer.Authorize(peer(uidAdmin), hostDev),
		ReasonDangerousDevice, "host_device /dev/sda")

	// 嵌套在 blockdev-add 的 file 节点里指向 /proc 同样拒绝。
	procBlock := qmp("blockdev-add", map[string]any{
		"driver": "raw",
		"file": map[string]any{
			"driver":   "file",
			"filename": "/proc/self/mem",
		},
	}, "pb")
	assertDenied(t, authorizer.Authorize(peer(uidAdmin), procBlock),
		ReasonDangerousDevice, "nested file /proc")

	// blockdev-add 指向登记目录之外，拒绝。
	outside := qmp("blockdev-add", map[string]any{
		"driver":   "raw",
		"filename": "/etc/libvirt/secrets.xml",
	}, "o1")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), outside),
		ReasonPathOutsideAllowed, "outside registered dir")

	// 词法 .. 穿越不能逃出登记目录。
	escape := qmp("blockdev-add", map[string]any{
		"driver":   "raw",
		"filename": "/srv/vm/operator/../../etc/passwd",
	}, "e1")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), escape),
		ReasonPathOutsideAllowed, "dot-dot escape")

	// 相对路径拒绝。
	relative := qmp("add-fd", map[string]any{"path": "disks/disk0.qcow2"}, "r1")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), relative),
		ReasonPathNotAbsolute, "relative path")

	// 越权使用其他角色的登记目录。
	adminDir := qmp("blockdev-add", map[string]any{
		"driver":   "raw",
		"filename": "/srv/vm/admin/disk0.qcow2",
	}, "a1")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), adminDir),
		ReasonPathOutsideAllowed, "operator touches admin dir")

	// 合规路径放行，嵌套节点一并检查。
	good := qmp("blockdev-add", map[string]any{
		"driver":   "raw",
		"filename": "/srv/vm/operator/disk0.qcow2",
		"file": map[string]any{
			"driver":   "file",
			"filename": "/srv/vm/operator/backing.qcow2",
		},
	}, "g1")
	assertAllowed(t, authorizer.Authorize(peer(uidOperator), good), "registered dir allowed")

	// getfd 路径同样限定在登记目录内。
	getfdOK := qmp("getfd", map[string]any{"path": "/srv/vm/operator/fd"}, "g2")
	assertAllowed(t, authorizer.Authorize(peer(uidOperator), getfdOK), "getfd inside dir")
	getfdBad := qmp("getfd", map[string]any{"path": "/srv/vm/admin/fd"}, "g3")
	assertDenied(t, authorizer.Authorize(peer(uidOperator), getfdBad),
		ReasonPathOutsideAllowed, "getfd outside dir")
}

// 场景 4：命令名与 driver 值区分大小写，大小写变形不能绕过限制。
func TestCaseSensitivity(t *testing.T) {
	authorizer := newTestAuthorizer(t)

	// 变形的协商命令不被识别为协商。
	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), qmp("QMP_CAPABILITIES", nil, 1)),
		ReasonCapabilitiesFirst,
		"uppercase QMP_CAPABILITIES is not negotiation")
	negotiate(t, authorizer, uidAdmin)
	negotiate(t, authorizer, uidReadonly)

	// 混合大小写命令不匹配 query- 前缀，只读角色仍被拒绝。
	assertDenied(t,
		authorizer.Authorize(peer(uidReadonly), qmp("Human-Monitor-Command", nil, 2)),
		ReasonReadonlyForbidden,
		"mixed-case command does not gain query- prefix")

	// QUERY-STATUS 大写变形不匹配 query- 前缀。
	assertDenied(t,
		authorizer.Authorize(peer(uidReadonly), qmp("QUERY-STATUS", nil, 3)),
		ReasonReadonlyForbidden,
		"uppercase QUERY-STATUS denied")
	assertAllowed(t,
		authorizer.Authorize(peer(uidReadonly), qmp("query-status", nil, 4)),
		"lowercase query-status allowed")

	// 大写 driver 不匹配危险 driver 名（driver 取值区分大小写）。
	upperDriver := qmp("device_add", map[string]any{
		"driver": "HOST_DEVICE",
		"path":   "/dev/sda",
	}, 5)
	assertAllowed(t, authorizer.Authorize(peer(uidAdmin), upperDriver),
		"driver name is case sensitive")

	// blockdev-add 名称大小写变形后不再是路径受限命令名。
	upperBlock := qmp("BLOCKDEV-ADD", map[string]any{
		"driver":   "file",
		"filename": "/srv/vm/admin/disk.qcow2",
	}, 6)
	assertAllowed(t, authorizer.Authorize(peer(uidAdmin), upperBlock),
		"BLOCKDEV-ADD casing bypasses name-based path rule")

	// 真正的小写 blockdev-add + file + /dev 组合仍被危险 driver 规则拦截。
	devBlock := qmp("blockdev-add", map[string]any{
		"driver":   "file",
		"filename": "/dev/null",
	}, 7)
	assertDenied(t, authorizer.Authorize(peer(uidAdmin), devBlock),
		ReasonDangerousDevice, "lowercase blockdev-add file /dev/null")
}

// 场景 5：合规命令端到端放行，id 原样透传，畸形输入被拒。
func TestCompliantCommands(t *testing.T) {
	authorizer := newTestAuthorizer(t)
	negotiate(t, authorizer, uidAdmin)
	negotiate(t, authorizer, uidReadonly)

	decision := authorizer.Authorize(peer(uidAdmin),
		qmp("query-name", nil, map[string]any{"tag": "vm-7"}))
	assertAllowed(t, decision, "admin query-name")
	if string(decision.ID) != `{"tag":"vm-7"}` {
		t.Errorf("id not preserved: %s", decision.ID)
	}
	if decision.Command != "query-name" {
		t.Errorf("command not recorded: %q", decision.Command)
	}

	assertAllowed(t,
		authorizer.Authorize(peer(uidReadonly), qmp("query-blockstats", nil, "ro")),
		"readonly query-blockstats")

	// 无 arguments 的普通命令。
	assertAllowed(t,
		authorizer.Authorize(peer(uidAdmin), qmp("query-kvm", nil, nil)),
		"no arguments field")

	// 畸形 JSON 与缺失 execute。
	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), json.RawMessage(`{"execute":`)),
		ReasonMalformedJSON, "malformed json")
	assertDenied(t,
		authorizer.Authorize(peer(uidAdmin), json.RawMessage(`{"arguments":{}}`)),
		ReasonMissingExecute, "missing execute")
}

// 批量消息逐条独立判定：一条被绝不影响后续命令的协商与判定。
func TestBatchIndependentDecisions(t *testing.T) {
	authorizer := newTestAuthorizer(t)

	// 全新会话的批量消息：未协商命令被拒、qmp_capabilities 成功、
	// 后续合规 query 放行、再后一条危险命令被拒——各自独立。
	batch := []json.RawMessage{
		qmp("query-status", nil, "b1"),
		qmp("qmp_capabilities", nil, "b2"),
		qmp("query-status", nil, "b3"),
		qmp("device_add", map[string]any{"driver": "host_device", "path": "/dev/sda"}, "b4"),
		qmp("query-status", nil, "b5"),
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch: %v", err)
	}

	decisions := authorizer.AuthorizeBatch(peer(uidAdmin), raw)
	if len(decisions) != len(batch) {
		t.Fatalf("got %d decisions, want %d", len(decisions), len(batch))
	}

	want := []struct {
		allowed bool
		reason  string
	}{
		{false, ReasonCapabilitiesFirst},
		{true, ""},
		{true, ""},
		{false, ReasonDangerousDevice},
		{true, ""},
	}
	for index, expected := range want {
		if decisions[index].Allowed != expected.allowed {
			t.Errorf("batch[%d] allowed=%v, want %v (reason=%q)",
				index, decisions[index].Allowed, expected.allowed, decisions[index].Reason)
		}
		if !expected.allowed && decisions[index].Reason != expected.reason {
			t.Errorf("batch[%d] reason=%q, want %q", index, decisions[index].Reason, expected.reason)
		}
		if string(decisions[index].ID) != string(mustRawID(t, batch[index])) {
			t.Errorf("batch[%d] id not preserved: %s", index, decisions[index].ID)
		}
	}

	// 单对象输入同样走批量入口。
	negotiate(t, authorizer, uidReadonly)
	single := authorizer.AuthorizeBatch(peer(uidReadonly), qmp("query-status", nil, "s"))
	if len(single) != 1 || !single[0].Allowed {
		t.Errorf("single-object batch should contain one allowed decision, got %+v", single)
	}
}

func mustRawID(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var probe qmpCommand
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal probe: %v", err)
	}
	return probe.ID
}

// 策略配置错误在构造期暴露。
func TestNewAuthorizerValidation(t *testing.T) {
	t.Run("nil policy", func(t *testing.T) {
		if _, err := NewAuthorizer(nil); err == nil {
			t.Error("expected error for nil policy")
		}
	})

	t.Run("relative allowed dir", func(t *testing.T) {
		_, err := NewAuthorizer(&Policy{
			Roles: map[string]*RolePolicy{
				RoleOperator: {Name: RoleOperator, AllowedDirs: []string{"relative/dir"}},
			},
		})
		if err == nil {
			t.Error("expected error for non-absolute allowed dir")
		}
	})

	t.Run("dangling role mapping", func(t *testing.T) {
		_, err := NewAuthorizer(&Policy{
			Roles: map[string]*RolePolicy{
				RoleAdmin: {Name: RoleAdmin, Admin: true},
			},
			UIDRoles: map[uint32]string{7: "ghost"},
		})
		if err == nil {
			t.Error("expected error for undefined role mapping")
		}
	})

	t.Run("dirs are cleaned", func(t *testing.T) {
		authorizer, err := NewAuthorizer(&Policy{
			Roles: map[string]*RolePolicy{
				RoleOperator: {Name: RoleOperator, AllowedDirs: []string{"/srv/vm/operator/"}},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := authorizer.policy.Roles[RoleOperator].AllowedDirs[0]
		if got != "/srv/vm/operator" {
			t.Errorf("allowed dir not cleaned: %q", got)
		}
	})
}
