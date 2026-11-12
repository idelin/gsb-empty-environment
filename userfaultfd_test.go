package netsec

import (
	"errors"
	"testing"
)

var (
	anonHeap   = Mapping{Start: 0x1000, End: 0x5000, Anonymous: true, Private: true}
	fileLibc   = Mapping{Start: 0x5000, End: 0x9000, Anonymous: false, Private: true, Path: "/lib/libc.so"}
	sharedAnon = Mapping{Start: 0x9000, End: 0xb000, Anonymous: true, Private: false}
)

func callerWithMaps(pid int, caps uint64) Caller {
	return Caller{PID: pid, Caps: caps, Mappings: []Mapping{anonHeap, fileLibc, sharedAnon}}
}

func mustCreate(t *testing.T, a *Authorizer, c Caller, flags CreateFlags) int {
	t.Helper()
	fd, err := a.Create(c, flags)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return fd
}

// 缺标志创建：无 CAP_SYS_PTRACE 时，无论 vm.unprivileged_userfaultfd
// 如何设置，都必须带 UFFD_USER_MODE_ONLY。
func TestCreateRequiresUserModeOnly(t *testing.T) {
	for _, sysctlOn := range []bool{false, true} {
		a := NewAuthorizer(Sysctl{UnprivilegedUserfaultfd: sysctlOn}, nil)
		unpriv := callerWithMaps(100, 0)

		if _, err := a.Create(unpriv, FlagCloexec|FlagNonblock); !errors.Is(err, ErrUserModeOnlyRequired) {
			t.Fatalf("sysctl=%v: create without UFFD_USER_MODE_ONLY = %v, want ErrUserModeOnlyRequired", sysctlOn, err)
		}
		if _, err := a.Create(unpriv, FlagUserModeOnly|FlagCloexec|FlagNonblock); err != nil {
			t.Fatalf("sysctl=%v: create with UFFD_USER_MODE_ONLY = %v, want nil", sysctlOn, err)
		}

		priv := callerWithMaps(101, CapSysPtrace)
		if _, err := a.Create(priv, FlagCloexec); err != nil {
			t.Fatalf("sysctl=%v: CAP_SYS_PTRACE create without flag = %v, want nil", sysctlOn, err)
		}
		if _, err := a.Create(priv, CreateFlags(1<<31)); !errors.Is(err, ErrUnknownCreateFlags) {
			t.Fatalf("sysctl=%v: unknown flags = %v, want ErrUnknownCreateFlags", sysctlOn, err)
		}
	}
}

// 范围越到文件映射：REGISTER 只允许落在调用者自己的匿名私有映射内。
func TestRegisterRangeMustStayAnonymousPrivate(t *testing.T) {
	a := NewAuthorizer(Sysctl{UnprivilegedUserfaultfd: true}, nil)
	c := callerWithMaps(200, 0)
	fd := mustCreate(t, a, c, FlagUserModeOnly)

	if err := a.Register(c, fd, 0x1000, 0x2000, ModeMissing); !errors.Is(err, ErrAPINotNegotiated) {
		t.Fatalf("register before UFFDIO_API = %v, want ErrAPINotNegotiated", err)
	}
	if err := a.API(c, fd, 0); err != nil {
		t.Fatalf("API: %v", err)
	}

	cases := []struct {
		name       string
		start, end uint64
		want       error
	}{
		{"inside anonymous private", 0x1000, 0x5000, nil},
		{"subrange of anonymous private", 0x2000, 0x3000, nil},
		{"crossing into file mapping", 0x3000, 0x7000, ErrRangeNotAnonPrivate},
		{"fully inside file mapping", 0x6000, 0x7000, ErrRangeNotAnonPrivate},
		{"crossing into shared mapping", 0x4000, 0xa000, ErrRangeNotAnonPrivate},
		{"unaligned start", 0x1800, 0x3000, ErrInvalidRange},
		{"empty range", 0x3000, 0x3000, ErrInvalidRange},
	}
	for _, tc := range cases {
		if err := a.Register(c, fd, tc.start, tc.end, ModeMissing); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

// WP 模式：MISSING 之外的模式需要额外许可，且须先经 UFFDIO_API 协商。
func TestWPModeRequiresGrantAndNegotiation(t *testing.T) {
	grantWP := func(c Caller, m RegisterMode) bool { return m == ModeWP && c.PID == 300 }
	a := NewAuthorizer(Sysctl{}, grantWP)
	granted := callerWithMaps(300, 0)
	denied := callerWithMaps(301, 0)

	// 无许可者连 UFFDIO_API 的 WP 特性都拿不到。
	fdDenied := mustCreate(t, a, denied, FlagUserModeOnly)
	if err := a.API(denied, fdDenied, FeaturePagefaultFlagWP); !errors.Is(err, ErrFeatureNotGranted) {
		t.Fatalf("API WP without grant = %v, want ErrFeatureNotGranted", err)
	}
	if err := a.API(denied, fdDenied, 0); err != nil {
		t.Fatalf("API without features: %v", err)
	}
	if err := a.Register(denied, fdDenied, 0x1000, 0x2000, ModeWP); !errors.Is(err, ErrModeNotNegotiated) {
		t.Fatalf("WP register without negotiation = %v, want ErrModeNotNegotiated", err)
	}

	// 有许可者：先协商 WP 特性，再注册 WP 范围。
	fd := mustCreate(t, a, granted, FlagUserModeOnly)
	if err := a.Register(granted, fd, 0x1000, 0x2000, ModeWP); !errors.Is(err, ErrAPINotNegotiated) {
		t.Fatalf("WP register before API = %v, want ErrAPINotNegotiated", err)
	}
	if err := a.API(granted, fd, FeaturePagefaultFlagWP); err != nil {
		t.Fatalf("API WP with grant: %v", err)
	}
	if err := a.Register(granted, fd, 0x1000, 0x2000, ModeMissing|ModeWP); err != nil {
		t.Fatalf("WP register with grant: %v", err)
	}

	// MINOR 不在许可范围内，即使已协商 WP。
	if err := a.Register(granted, fd, 0x1000, 0x2000, ModeMinor); !errors.Is(err, ErrModeNotNegotiated) {
		t.Fatalf("MINOR register without feature = %v, want ErrModeNotNegotiated", err)
	}
	if err := a.API(granted, fd, FeatureMinorShmem); !errors.Is(err, ErrFeatureNotGranted) {
		t.Fatalf("API MINOR without grant = %v, want ErrFeatureNotGranted", err)
	}
}

// 传递描述符：SCM_RIGHTS 之后，已注册范围的权限判定不变。
func TestPassFDPreservesRegisteredVerdicts(t *testing.T) {
	// 只有创建者（PID 400）持有 WP 许可；接收方没有任何许可。
	grant := func(c Caller, m RegisterMode) bool { return c.PID == 400 }
	a := NewAuthorizer(Sysctl{UnprivilegedUserfaultfd: true}, grant)

	creator := callerWithMaps(400, 0)
	recipient := callerWithMaps(401, 0)

	fd := mustCreate(t, a, creator, FlagUserModeOnly|FlagCloexec)
	if err := a.API(creator, fd, FeaturePagefaultFlagWP); err != nil {
		t.Fatalf("API: %v", err)
	}
	if err := a.Register(creator, fd, 0x1000, 0x4000, ModeMissing|ModeWP); err != nil {
		t.Fatalf("Register: %v", err)
	}

	newFD, err := a.PassFD(creator, recipient, fd)
	if err != nil {
		t.Fatalf("PassFD: %v", err)
	}

	// 已注册范围：创建者与接收方得到完全相同的判定。
	for _, c := range []Caller{creator, recipient} {
		useFD := fd
		if c.PID == recipient.PID {
			useFD = newFD
		}
		if err := a.AuthorizeRegistered(c, useFD, 0x2000, 0x3000, ModeWP); err != nil {
			t.Fatalf("holder %d: registered WP range = %v, want nil", c.PID, err)
		}
		if err := a.AuthorizeRegistered(c, useFD, 0x4000, 0x5000, ModeMissing); !errors.Is(err, ErrRangeNotRegistered) {
			t.Fatalf("holder %d: unregistered range = %v, want ErrRangeNotRegistered", c.PID, err)
		}
	}

	// 接收方不能冒用创建者的描述符号，也不能凭传递来的描述符注册新 WP 范围。
	if _, err := a.PassFD(recipient, creator, fd); !errors.Is(err, ErrBadDescriptor) {
		t.Fatalf("re-passing foreign fd = %v, want ErrBadDescriptor", err)
	}
	if err := a.Register(recipient, newFD, 0x1000, 0x2000, ModeWP); !errors.Is(err, ErrModeNotGranted) {
		t.Fatalf("recipient new WP register = %v, want ErrModeNotGranted", err)
	}
	if err := a.Register(recipient, newFD, 0x1000, 0x2000, ModeMissing); err != nil {
		t.Fatalf("recipient MISSING register on own mapping = %v, want nil", err)
	}
}
