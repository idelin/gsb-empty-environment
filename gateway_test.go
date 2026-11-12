package netsec

import "testing"

// testEnv 搭出主机侧与沙箱侧的命名空间环境。
type testEnv struct {
	root *UserNamespace // 初始用户命名空间

	hostMount *MountNamespace
	hostUTS   *UTSNamespace
	hostNet   *NetNamespace
	hostFS    *Superblock

	sandbox      *Credentials // 沙箱内任务：子用户命名空间中持有全部能力
	sandboxMount *MountNamespace
	sandboxUTS   *UTSNamespace
	sandboxNet   *NetNamespace
	sandboxFS    *Superblock
}

func newTestEnv() *testEnv {
	root := NewRootUserNamespace()
	// 宿主机上的普通用户（uid 1000，无任何能力）。
	unpriv := &Credentials{UserNS: root, EUID: 1000}
	// unshare(CLONE_NEWUSER) 后在子命名空间内获得全部能力——这正是
	// “只看能力位”的授权代码会被骗过的凭据形态。
	sandbox := unpriv.UnshareUserNS()

	return &testEnv{
		root:         root,
		hostMount:    &MountNamespace{Owner: root},
		hostUTS:      &UTSNamespace{Owner: root},
		hostNet:      &NetNamespace{Owner: root},
		hostFS:       &Superblock{Owner: root},
		sandbox:      sandbox,
		sandboxMount: &MountNamespace{Owner: sandbox.UserNS},
		sandboxUTS:   &UTSNamespace{Owner: sandbox.UserNS},
		sandboxNet:   &NetNamespace{Owner: sandbox.UserNS},
		sandboxFS:    &Superblock{Owner: sandbox.UserNS},
	}
}

func TestSandboxCapsValidOnlyInsideOwnNamespace(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}

	// 前提：沙箱任务确实持有相关能力位（在其子命名空间内）。
	for _, cap := range []Capability{CapSysAdmin, CapMknod, CapNetAdmin} {
		if !env.sandbox.Effective.Has(cap) {
			t.Fatalf("sandbox creds should hold %d in effective set", cap)
		}
	}

	t.Run("mount on host mount namespace denied", func(t *testing.T) {
		if err := gw.Mount(env.sandbox, env.hostMount, "/dev/sda1", "/mnt", "ext4"); err != ErrNotPermitted {
			t.Fatalf("got %v, want EPERM", err)
		}
	})
	t.Run("mount in own mount namespace allowed", func(t *testing.T) {
		if err := gw.Mount(env.sandbox, env.sandboxMount, "tmpfs", "/tmp", "tmpfs"); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
	t.Run("set host hostname denied", func(t *testing.T) {
		if err := gw.SetHostname(env.sandbox, env.hostUTS, "pwned"); err != ErrNotPermitted {
			t.Fatalf("got %v, want EPERM", err)
		}
	})
	t.Run("set own hostname allowed", func(t *testing.T) {
		if err := gw.SetHostname(env.sandbox, env.sandboxUTS, "sandbox"); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
	t.Run("mknod on host filesystem denied", func(t *testing.T) {
		if err := gw.Mknod(env.sandbox, env.hostFS, "/dev/sda", 0x801); err != ErrNotPermitted {
			t.Fatalf("got %v, want EPERM", err)
		}
	})
	t.Run("mknod on own filesystem allowed", func(t *testing.T) {
		if err := gw.Mknod(env.sandbox, env.sandboxFS, "/dev/null", 0x103); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
	t.Run("write host net sysctl denied", func(t *testing.T) {
		err := gw.WriteNetSysctl(env.sandbox, env.hostNet, "net.ipv4.ip_forward", "1")
		if err != ErrNotPermitted {
			t.Fatalf("got %v, want EPERM", err)
		}
	})
	t.Run("write own net sysctl allowed", func(t *testing.T) {
		err := gw.WriteNetSysctl(env.sandbox, env.sandboxNet, "net.ipv4.ip_forward", "1")
		if err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
}

func TestAncestorNamespaceCapsCoverDescendantResources(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}
	// 初始命名空间中的特权守护进程。
	rootAdmin := &Credentials{
		UserNS:    env.root,
		Effective: FullCapSet,
		Permitted: FullCapSet,
		EUID:      0,
	}
	// 祖先命名空间持有的能力覆盖后代命名空间拥有的资源。
	if err := gw.Mount(rootAdmin, env.sandboxMount, "tmpfs", "/x", "tmpfs"); err != nil {
		t.Fatalf("root admin should mount in descendant mount ns: %v", err)
	}
	if err := gw.WriteNetSysctl(rootAdmin, env.sandboxNet, "net.ipv4.ip_forward", "0"); err != nil {
		t.Fatalf("root admin should write descendant net sysctl: %v", err)
	}
	if err := gw.SetHostname(rootAdmin, env.hostUTS, "host"); err != nil {
		t.Fatalf("root admin should set host hostname: %v", err)
	}
}

func TestSetnsIntoChildDropsPowerOverHost(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}
	rootAdmin := &Credentials{
		UserNS:    env.root,
		Effective: FullCapSet,
		Permitted: FullCapSet,
		EUID:      0,
	}
	// setns 进入沙箱的子用户命名空间，获得其中的全部能力。
	joined, err := rootAdmin.Setns(env.sandbox.UserNS)
	if err != nil {
		t.Fatalf("setns should succeed: %v", err)
	}
	if !joined.Effective.Has(CapSysAdmin) {
		t.Fatal("joined creds should hold full caps in child ns")
	}
	// 但迁入后对宿主机资源的操作必须被拒绝。
	if err := gw.Mount(joined, env.hostMount, "tmpfs", "/mnt", "tmpfs"); err != ErrNotPermitted {
		t.Fatalf("mount on host after setns: got %v, want EPERM", err)
	}
	if err := gw.SetHostname(joined, env.hostUTS, "pwned"); err != ErrNotPermitted {
		t.Fatalf("set host hostname after setns: got %v, want EPERM", err)
	}
	if err := gw.WriteNetSysctl(joined, env.hostNet, "net.ipv4.ip_forward", "1"); err != ErrNotPermitted {
		t.Fatalf("write host sysctl after setns: got %v, want EPERM", err)
	}
	// 对子命名空间拥有的资源仍然放行。
	if err := gw.Mount(joined, env.sandboxMount, "tmpfs", "/y", "tmpfs"); err != nil {
		t.Fatalf("mount in own ns after setns: %v", err)
	}
	// 无权限任务不能借 setns 提权进入初始命名空间。
	if _, err := env.sandbox.Setns(env.root); err != ErrNotPermitted {
		t.Fatalf("sandbox setns into root ns: got %v, want EPERM", err)
	}
}

func TestGrandchildNamespaceHierarchy(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}
	// 沙箱任务再创建孙命名空间。
	grandchild := env.sandbox.UnshareUserNS()
	grandchildMount := &MountNamespace{Owner: grandchild.UserNS}

	// 父命名空间的能力覆盖孙命名空间的资源。
	if err := gw.Mount(env.sandbox, grandchildMount, "tmpfs", "/z", "tmpfs"); err != nil {
		t.Fatalf("parent ns caps should cover grandchild resource: %v", err)
	}
	// 孙命名空间的能力不能反向作用于父命名空间的资源。
	if err := gw.Mount(grandchild, env.sandboxMount, "tmpfs", "/z", "tmpfs"); err != ErrNotPermitted {
		t.Fatalf("grandchild mount in parent ns: got %v, want EPERM", err)
	}
	// 更不能作用于初始命名空间的资源。
	if err := gw.SetHostname(grandchild, env.hostUTS, "pwned"); err != ErrNotPermitted {
		t.Fatalf("grandchild set host hostname: got %v, want EPERM", err)
	}
}

func TestNoNewPrivsBlocksExecEscalation(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}
	// 沙箱任务设置 no_new_privs 后 exec 一个 setuid-root 且带文件能力的程序。
	env.sandbox.NoNewPrivs = true
	after := env.sandbox.Exec(&FileCaps{
		SetUID:    true,
		UID:       0,
		Permitted: FullCapSet,
		Effective: true,
	})
	if after.EUID != 1000 {
		t.Fatalf("no_new_privs exec must not change euid, got %d", after.EUID)
	}
	if after.UserNS != env.sandbox.UserNS {
		t.Fatal("no_new_privs exec must not change user namespace")
	}
	// 即便 exec 后能力位仍在，对宿主机资源依旧无效。
	if err := gw.Mount(after, env.hostMount, "tmpfs", "/mnt", "tmpfs"); err != ErrNotPermitted {
		t.Fatalf("mount on host after no_new_privs exec: got %v, want EPERM", err)
	}

	// 对照：未设 no_new_privs 时 exec 可以按文件能力提权（在初始命名空间中）。
	hostUser := &Credentials{UserNS: env.root, EUID: 1000}
	escalated := hostUser.Exec(&FileCaps{SetUID: true, UID: 0, Permitted: FullCapSet, Effective: true})
	if escalated.EUID != 0 || !escalated.Effective.Has(CapSysAdmin) {
		t.Fatal("exec without no_new_privs should apply file caps")
	}
	if err := gw.Mount(escalated, env.hostMount, "tmpfs", "/mnt", "tmpfs"); err != nil {
		t.Fatalf("escalated host task should mount on host: %v", err)
	}
}

func TestCreatorRuleGrantsCapsOverOwnedChild(t *testing.T) {
	env := newTestEnv()
	gw := Gateway{}
	// 内核规则：用户命名空间的创建者（父命名空间中 euid 匹配）对其拥有
	// 全部能力，即使父命名空间中的凭据未置任何能力位。
	creator := &Credentials{UserNS: env.root, EUID: 1000}
	if err := gw.Mount(creator, env.sandboxMount, "tmpfs", "/w", "tmpfs"); err != nil {
		t.Fatalf("creator should have full caps over owned child ns: %v", err)
	}
	// 但创建者对初始命名空间的资源依然无权。
	if err := gw.Mount(creator, env.hostMount, "tmpfs", "/w", "tmpfs"); err != ErrNotPermitted {
		t.Fatalf("creator mount on host: got %v, want EPERM", err)
	}
}
