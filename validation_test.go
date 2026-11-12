package netsec_test

import (
	"strings"
	"testing"

	"example.org/netsec/q0380"
)

func TestNewKeyValidation(t *testing.T) {
	if _, err := netsec.NewKey(1, 0b100, [2]uint64{}); err == nil {
		t.Fatal("arg mask beyond modeled args must fail")
	}
	if _, err := netsec.NewKey(1, 0b01, [2]uint64{0, 9}); err == nil {
		t.Fatal("nonzero value on a masked-out arg position must fail")
	}
	key, err := netsec.NewKey(7, 0b11, [2]uint64{3, 4})
	if err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if key.Syscall != 7 || key.ArgMask != 0b11 || key.Args != [2]uint64{3, 4} {
		t.Fatalf("unexpected key: %+v", key)
	}
}

func TestNewTableRejectsInvalidInput(t *testing.T) {
	if _, err := netsec.NewTable("bad-default", nil, netsec.Decision{Action: netsec.Action(99)}); err == nil {
		t.Fatal("invalid default action must fail")
	}
	key := mustKey(t, 1, 1)
	if _, err := netsec.NewTable("bad-rule", map[netsec.Key]netsec.Decision{
		key: {Action: netsec.Action(7)},
	}, netsec.Decision{Action: netsec.Allow}); err == nil {
		t.Fatal("invalid rule action must fail")
	}
}

func TestNewHostAndThreadValidation(t *testing.T) {
	if _, err := netsec.NewHost(nil, nil); err == nil {
		t.Fatal("nil domain must fail")
	}
	host, err := netsec.NewHost(testDomain(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.AddThread("dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := host.AddThread("dup"); err == nil {
		t.Fatal("duplicate thread id must fail")
	}
	if err := host.RemoveThread("missing"); err == nil ||
		!strings.Contains(err.Error(), "not registered") {
		t.Fatalf("removing unknown thread must report error, got %v", err)
	}
	if err := host.Install(nil); err == nil {
		t.Fatal("installing nil table must fail")
	}
	if _, err := netsec.NewChain().Append(nil); err == nil {
		t.Fatal("appending nil table must fail")
	}
}

func TestDomainRejectsEmptySyscalls(t *testing.T) {
	if _, err := netsec.NewDomain(nil, [2][]uint64{{1}, {2}}); err == nil {
		t.Fatal("domain without syscalls must fail")
	}
}

func TestActionStringAndValid(t *testing.T) {
	cases := map[netsec.Action]string{
		netsec.Allow:  "allow",
		netsec.Notify: "notify",
		netsec.Deny:   "deny",
		netsec.Kill:   "kill",
	}
	for action, want := range cases {
		if !action.Valid() || action.String() != want {
			t.Fatalf("action %d: got %q valid=%v", action, action.String(), action.Valid())
		}
	}
	if (netsec.Action(200)).Valid() {
		t.Fatal("unknown action must be invalid")
	}
}

func TestAccessorsAndErrorText(t *testing.T) {
	domain := testDomain(t)
	if got := domain.Syscalls(); len(got) != 3 || got[0] != 1 {
		t.Fatalf("Syscalls accessor: %+v", got)
	}
	if got := domain.ArgValues(0); len(got) != 2 {
		t.Fatalf("ArgValues accessor: %+v", got)
	}
	if domain.ArgValues(9) != nil {
		t.Fatal("out-of-range ArgValues must return nil")
	}

	key := mustKey(t, 1, 0, 10)
	table := mustTable(t, "named", map[netsec.Key]netsec.Decision{
		key: {Action: netsec.Notify, Tag: "x"},
	}, netsec.Decision{Action: netsec.Kill})
	if table.Name() != "named" {
		t.Fatalf("Name accessor: %q", table.Name())
	}
	if table.Default().Action != netsec.Kill {
		t.Fatalf("Default accessor: %+v", table.Default())
	}

	chain := netsec.NewChain()
	extended, err := chain.Append(table)
	if err != nil {
		t.Fatal(err)
	}
	if extended.TableAt(0) != table {
		t.Fatal("TableAt must return the installed table")
	}
	if chain.TableAt(0) != nil || chain.TableAt(-1) != nil {
		t.Fatal("TableAt out of range must return nil")
	}

	// 错误文本与 Unwrap 必须可被宿主调用方稳定使用。
	widening := &netsec.WideningError{
		Request: key,
		Before:  netsec.Decision{Action: netsec.Deny},
		After:   netsec.Decision{Action: netsec.Allow},
	}
	if !strings.Contains(widening.Error(), "widen") {
		t.Fatalf("widening error text: %q", widening.Error())
	}
	installErr := &netsec.InstallError{FailedThread: "t9", RolledBack: true}
	if !strings.Contains(installErr.Error(), "rolled back") {
		t.Fatalf("install error text: %q", installErr.Error())
	}
}
