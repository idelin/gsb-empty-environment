package netsec

import (
	"errors"
	"testing"
)

func newValidator(t *testing.T) *Validator {
	t.Helper()
	return NewValidator(DefaultRegistry())
}

func TestProtectionOverride(t *testing.T) {
	cases := []struct {
		name string
		seq  []Command
	}{
		{
			name: "nosuid then suid string",
			seq: []Command{
				{Cmd: CmdSetFlag, Key: "nosuid"},
				{Cmd: CmdSetString, Key: "suid", Value: "1"},
			},
		},
		{
			name: "nosuid set twice",
			seq: []Command{
				{Cmd: CmdSetFlag, Key: "nosuid"},
				{Cmd: CmdSetFlag, Key: "nosuid"},
			},
		},
		{
			name: "nodev then dev flag",
			seq: []Command{
				{Cmd: CmdSetFlag, Key: "nodev"},
				{Cmd: CmdSetFlag, Key: "dev"},
			},
		},
		{
			name: "noexec then exec string",
			seq: []Command{
				{Cmd: CmdSetFlag, Key: "noexec"},
				{Cmd: CmdSetString, Key: "exec", Value: "1"},
			},
		},
		{
			name: "nosuid then suid after reconfigure",
			seq: []Command{
				{Cmd: CmdSetFlag, Key: "nosuid"},
				{Cmd: CmdReconfigure},
				{Cmd: CmdSetString, Key: "suid", Value: "1"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newValidator(t).Validate("tmpfs", tc.seq)
			if !errors.Is(err, ErrProtectionOverride) {
				t.Fatalf("want ErrProtectionOverride, got %v", err)
			}
		})
	}
}

func TestSetAfterCreate(t *testing.T) {
	seq := []Command{
		{Cmd: CmdSetFlag, Key: "nosuid"},
		{Cmd: CmdCreate},
		{Cmd: CmdSetString, Key: "size", Value: "64m"},
	}
	_, err := newValidator(t).Validate("tmpfs", seq)
	if !errors.Is(err, ErrSetAfterCreate) {
		t.Fatalf("want ErrSetAfterCreate, got %v", err)
	}
}

func TestUnregisteredFD(t *testing.T) {
	reg := NewRegistry()
	reg.Register("zfs", map[string]OptionSpec{
		"nosuid":  {Cmds: []Cmd{CmdSetFlag}},
		"snapdir": {Cmds: []Cmd{CmdSetFd}},
	})
	v := NewValidator(reg)

	seq := []Command{
		{Cmd: CmdSetFlag, Key: "nosuid"},
		{Cmd: CmdSetFd, Key: "snapdir", Fd: 7},
		{Cmd: CmdCreate},
	}
	if _, err := v.Validate("zfs", seq); !errors.Is(err, ErrUnregisteredFD) {
		t.Fatalf("want ErrUnregisteredFD, got %v", err)
	}

	v.RegisterFD(7)
	if _, err := v.Validate("zfs", seq); err != nil {
		t.Fatalf("registered fd should pass, got %v", err)
	}

	v.UnregisterFD(7)
	if _, err := v.Validate("zfs", seq); !errors.Is(err, ErrUnregisteredFD) {
		t.Fatalf("unregistered fd should fail again, got %v", err)
	}
}

func TestCompliantSequence(t *testing.T) {
	seq := []Command{
		{Cmd: CmdSetString, Key: "size", Value: "64m"},
		{Cmd: CmdSetString, Key: "mode", Value: "0700"},
		{Cmd: CmdSetFlag, Key: "nosuid"},
		{Cmd: CmdSetFlag, Key: "nodev"},
		{Cmd: CmdSetFlag, Key: "noexec"},
		{Cmd: CmdSetFlag, Key: "ro"},
		{Cmd: CmdCreate},
	}
	res, err := newValidator(t).Validate("tmpfs", seq)
	if err != nil {
		t.Fatalf("compliant sequence rejected: %v", err)
	}
	want := MountAttrNoSuid | MountAttrNoDev | MountAttrNoExec | MountAttrRdOnly
	if res.Declared != want {
		t.Fatalf("declared = %#x, want %#x", uint64(res.Declared), uint64(want))
	}

	// fsmount with equal or stronger attributes is accepted.
	if err := res.ValidateMountAttr(want | MountAttrNoAtime); err != nil {
		t.Fatalf("stronger attrs rejected: %v", err)
	}
	// fsmount missing a declared protection bit is rejected.
	weak := MountAttrNoSuid | MountAttrNoDev | MountAttrRdOnly
	if err := res.ValidateMountAttr(weak); !errors.Is(err, ErrWeakMountAttr) {
		t.Fatalf("want ErrWeakMountAttr, got %v", err)
	}
}

func TestReconfigureOnlyKey(t *testing.T) {
	createPhase := []Command{
		{Cmd: CmdSetString, Key: "resize", Value: "large"},
		{Cmd: CmdCreate},
	}
	if _, err := newValidator(t).Validate("ext4", createPhase); !errors.Is(err, ErrReconfigureOnlyKey) {
		t.Fatalf("want ErrReconfigureOnlyKey, got %v", err)
	}

	reconfigurePhase := []Command{
		{Cmd: CmdReconfigure},
		{Cmd: CmdSetString, Key: "resize", Value: "large"},
	}
	if _, err := newValidator(t).Validate("ext4", reconfigurePhase); err != nil {
		t.Fatalf("reconfigure-phase key should pass, got %v", err)
	}
}

func TestWhitelistAndFSType(t *testing.T) {
	if _, err := newValidator(t).Validate("afs", nil); !errors.Is(err, ErrUnknownFSType) {
		t.Fatalf("want ErrUnknownFSType, got %v", err)
	}
	seq := []Command{{Cmd: CmdSetString, Key: "notanoption", Value: "x"}}
	if _, err := newValidator(t).Validate("tmpfs", seq); !errors.Is(err, ErrOptionNotAllowed) {
		t.Fatalf("want ErrOptionNotAllowed, got %v", err)
	}
	// Wrong command form for a whitelisted key.
	badForm := []Command{{Cmd: CmdSetString, Key: "nosuid", Value: "1"}}
	if _, err := newValidator(t).Validate("tmpfs", badForm); !errors.Is(err, ErrCommandNotAllowed) {
		t.Fatalf("want ErrCommandNotAllowed, got %v", err)
	}
}

func TestBadCommandSequence(t *testing.T) {
	doubleCreate := []Command{{Cmd: CmdCreate}, {Cmd: CmdCreate}}
	if _, err := newValidator(t).Validate("tmpfs", doubleCreate); !errors.Is(err, ErrBadCommandSequence) {
		t.Fatalf("want ErrBadCommandSequence, got %v", err)
	}
	createThenReconfigure := []Command{{Cmd: CmdCreate}, {Cmd: CmdReconfigure}}
	if _, err := newValidator(t).Validate("tmpfs", createThenReconfigure); !errors.Is(err, ErrBadCommandSequence) {
		t.Fatalf("want ErrBadCommandSequence, got %v", err)
	}
}
