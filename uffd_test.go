package uffd_test

import (
	"errors"
	"testing"

	uffd "prescreen.local/ps-54f6a34a3b"
)

const page = uintptr(0x1000)

func anon(start, end uintptr, owner uffd.ProcessID) uffd.Mapping {
	return uffd.Mapping{Start: start, End: end, Kind: uffd.AnonymousPrivate, Owner: owner}
}

func fileMapping(start, end uintptr, owner uffd.ProcessID) uffd.Mapping {
	return uffd.Mapping{Start: start, End: end, Kind: uffd.FilePrivate, Owner: owner}
}

func mustCreate(t *testing.T, policy *uffd.Authorizer, caller uffd.Caller, flags uffd.CreateFlags) *uffd.Descriptor {
	t.Helper()
	fd, err := policy.Create(caller, flags)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return fd
}

func mustRegister(t *testing.T, fd *uffd.Descriptor, caller uffd.Caller, start, length uintptr, mode uffd.RegisterMode, mappings []uffd.Mapping) {
	t.Helper()
	if err := fd.RegisterRange(caller, start, length, 0, mode, mappings); err != nil {
		t.Fatalf("RegisterRange(%#x,%#x,%s): %v", start, length, mode, err)
	}
}

func TestCreateFlagsAndCapability(t *testing.T) {
	unprivileged := uffd.Caller{Process: 1}
	privileged := uffd.Caller{Process: 2, Caps: uffd.Capabilities{CAPSysPtrace: true}}

	tests := []struct {
		name    string
		sysctl  uffd.Sysctl
		caller  uffd.Caller
		flags   uffd.CreateFlags
		wantErr error
	}{
		{
			name:    "unprivileged missing user-mode-only",
			sysctl:  uffd.UserfaultfdEnabled,
			caller:  unprivileged,
			flags:   uffd.CloseOnExec | uffd.NonBlock,
			wantErr: uffd.ErrUserModeOnlyRequired,
		},
		{
			name:   "unprivileged with user-mode-only",
			sysctl: uffd.UserfaultfdEnabled,
			caller: unprivileged,
			flags:  uffd.UserModeOnly | uffd.CloseOnExec | uffd.NonBlock,
		},
		{
			name:    "sysctl disables unprivileged even with flag",
			sysctl:  uffd.UserfaultfdDisabled,
			caller:  unprivileged,
			flags:   uffd.UserModeOnly,
			wantErr: uffd.ErrUnprivilegedDisabled,
		},
		{
			name:   "capability bypasses flag requirement",
			sysctl: uffd.UserfaultfdEnabled,
			caller: privileged,
			flags:  uffd.CloseOnExec,
		},
		{
			name:   "capability bypasses disabled sysctl",
			sysctl: uffd.UserfaultfdDisabled,
			caller: privileged,
			flags:  0,
		},
		{
			name:    "unknown flag is rejected",
			sysctl:  uffd.UserfaultfdEnabled,
			caller:  privileged,
			flags:   uffd.CreateFlags(1 << 30),
			wantErr: uffd.ErrUnknownCreateFlag,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := uffd.NewAuthorizer(tc.sysctl)
			fd, err := policy.Create(tc.caller, tc.flags)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Create error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if fd != nil {
					t.Fatal("failed Create returned a descriptor")
				}
				return
			}
			if fd == nil {
				t.Fatal("successful Create returned nil")
			}
			if got := fd.Flags(); got != tc.flags {
				t.Fatalf("Flags() = %#x, want %#x", got, tc.flags)
			}
			if got := fd.UserModeOnly(); got != (tc.flags&uffd.UserModeOnly != 0) {
				t.Fatalf("UserModeOnly() = %v", got)
			}
		})
	}
}

func TestRegisterShapeAndMappingChecks(t *testing.T) {
	policy := uffd.NewAuthorizer(uffd.UserfaultfdEnabled)
	caller := uffd.Caller{Process: 100}

	base := uintptr(0x10000)
	valid := []uffd.Mapping{anon(base, base+4*page, caller.Process)}

	tests := []struct {
		name     string
		start    uintptr
		length   uintptr
		end      uintptr
		mappings []uffd.Mapping
		wantErr  error
	}{
		{
			name:     "zero length",
			start:    base,
			mappings: valid,
			wantErr:  uffd.ErrZeroLength,
		},
		{
			name:     "unaligned start",
			start:    base + 0x80,
			length:   page,
			mappings: valid,
			wantErr:  uffd.ErrUnaligned,
		},
		{
			name:     "unaligned length",
			start:    base,
			length:   page + 0x80,
			mappings: valid,
			wantErr:  uffd.ErrUnaligned,
		},
		{
			name:    "start plus length overflow",
			start:   ^uintptr(0) &^ (page - 1),
			length:  2 * page,
			wantErr: uffd.ErrRangeOverflow,
		},
		{
			name:     "end disagrees with length",
			start:    base,
			length:   page,
			end:      base + 2*page,
			mappings: valid,
			wantErr:  uffd.ErrInvalidRange,
		},
		{
			name:     "file-backed mapping",
			start:    base,
			length:   page,
			mappings: []uffd.Mapping{fileMapping(base, base+4*page, caller.Process)},
			wantErr:  uffd.ErrNotAnonymousPrivate,
		},
		{
			name:   "range crosses into file mapping",
			start:  base,
			length: 2 * page,
			mappings: []uffd.Mapping{
				anon(base, base+page, caller.Process),
				fileMapping(base+page, base+2*page, caller.Process),
			},
			wantErr: uffd.ErrNotAnonymousPrivate,
		},
		{
			name:   "range crosses hole",
			start:  base,
			length: 3 * page,
			mappings: []uffd.Mapping{
				anon(base, base+page, caller.Process),
				anon(base+2*page, base+3*page, caller.Process),
			},
			wantErr: uffd.ErrUnmappedHole,
		},
		{
			name:     "explicit hole",
			start:    base,
			length:   page,
			mappings: []uffd.Mapping{{Start: base, End: base + page, Kind: uffd.Unmapped, Owner: caller.Process}},
			wantErr:  uffd.ErrUnmappedHole,
		},
		{
			name:     "mapping belongs to another process",
			start:    base,
			length:   page,
			mappings: []uffd.Mapping{anon(base, base+page, 200)},
			wantErr:  uffd.ErrNotOwner,
		},
		{
			name:     "mapping ends before range",
			start:    base,
			length:   2 * page,
			mappings: []uffd.Mapping{anon(base, base+page, caller.Process)},
			wantErr:  uffd.ErrUnmappedHole,
		},
		{
			name:     "valid range",
			start:    base,
			length:   2 * page,
			mappings: valid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fd := mustCreate(t, policy, caller, uffd.UserModeOnly)
			err := fd.RegisterRange(caller, tc.start, tc.length, tc.end, uffd.ModeMissing, tc.mappings)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("RegisterRange error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestRegisterModesAndPermissions(t *testing.T) {
	const base = uintptr(0x20000)
	mappings := []uffd.Mapping{anon(base, base+8*page, 100)}
	plain := uffd.Caller{Process: 100}
	wpCaller := uffd.Caller{Process: 100, Caps: uffd.Capabilities{WriteProtect: true}}
	minorCaller := uffd.Caller{Process: 100, Caps: uffd.Capabilities{Minor: true}}

	t.Run("WP needs extra permission", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), plain, uffd.UserModeOnly)
		err := fd.RegisterRange(plain, base, page, 0, uffd.ModeWriteProtect, mappings)
		if !errors.Is(err, uffd.ErrPermissionDenied) {
			t.Fatalf("WP register error = %v, want ErrPermissionDenied", err)
		}
	})

	t.Run("WP caller grant", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), wpCaller, uffd.UserModeOnly)
		mustRegister(t, fd, wpCaller, base, page, uffd.ModeWriteProtect, mappings)
	})

	t.Run("WP policy grant", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled, uffd.WithWriteProtect()), plain, uffd.UserModeOnly)
		mustRegister(t, fd, plain, base, page, uffd.ModeWriteProtect, mappings)
	})

	t.Run("MINOR needs extra permission", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), plain, uffd.UserModeOnly)
		err := fd.RegisterRange(plain, base, page, 0, uffd.ModeMinor, mappings)
		if !errors.Is(err, uffd.ErrPermissionDenied) {
			t.Fatalf("MINOR register error = %v, want ErrPermissionDenied", err)
		}
	})

	t.Run("MINOR caller grant", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), minorCaller, uffd.UserModeOnly)
		mustRegister(t, fd, minorCaller, base, page, uffd.ModeMinor, mappings)
	})

	t.Run("zero and unknown modes", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), plain, uffd.UserModeOnly)
		if err := fd.RegisterRange(plain, base, page, 0, 0, mappings); !errors.Is(err, uffd.ErrModeRequired) {
			t.Fatalf("zero mode error = %v", err)
		}
		if err := fd.RegisterRange(plain, base, page, 0, uffd.RegisterMode(1<<20), mappings); !errors.Is(err, uffd.ErrUnknownMode) {
			t.Fatalf("unknown mode error = %v", err)
		}
	})

	t.Run("copy and continue require matching modes", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), wpCaller, uffd.UserModeOnly)
		mustRegister(t, fd, wpCaller, base, page, uffd.ModeWriteProtect, mappings)
		if err := fd.CopyRange(wpCaller, base, page, 0); !errors.Is(err, uffd.ErrModeMismatch) {
			t.Fatalf("copy on WP-only error = %v", err)
		}
		if err := fd.WriteProtectRange(wpCaller, base, page, 0, true); err != nil {
			t.Fatalf("write protect WP range: %v", err)
		}
		if err := fd.WakeRange(wpCaller, base, page, 0); err != nil {
			t.Fatalf("wake WP range: %v", err)
		}
	})

	t.Run("write protect missing-only range", func(t *testing.T) {
		fd := mustCreate(t, uffd.NewAuthorizer(uffd.UserfaultfdEnabled), wpCaller, uffd.UserModeOnly)
		mustRegister(t, fd, wpCaller, base, page, uffd.ModeMissing, mappings)
		err := fd.WriteProtectRange(wpCaller, base, page, 0, true)
		if !errors.Is(err, uffd.ErrModeMismatch) {
			t.Fatalf("write protect missing range error = %v", err)
		}
	})
}

func TestRegistrationOverlapAndUnregister(t *testing.T) {
	policy := uffd.NewAuthorizer(uffd.UserfaultfdEnabled)
	caller := uffd.Caller{Process: 100}
	const base = uintptr(0x30000)
	mappings := []uffd.Mapping{anon(base, base+8*page, caller.Process)}

	t.Run("partial overlap rejected", func(t *testing.T) {
		fd := mustCreate(t, policy, caller, uffd.UserModeOnly)
		mustRegister(t, fd, caller, base, 2*page, uffd.ModeMissing, mappings)
		err := fd.RegisterRange(caller, base+page, 2*page, 0, uffd.ModeMissing, mappings)
		if !errors.Is(err, uffd.ErrPartialOverlap) {
			t.Fatalf("overlap error = %v", err)
		}
	})

	t.Run("exact duplicate is idempotent", func(t *testing.T) {
		fd := mustCreate(t, policy, caller, uffd.UserModeOnly)
		mustRegister(t, fd, caller, base, 2*page, uffd.ModeMissing, mappings)
		mustRegister(t, fd, caller, base, 2*page, uffd.ModeMissing, mappings)
		if got := len(fd.Registrations()); got != 1 {
			t.Fatalf("registrations = %d, want 1", got)
		}
	})

	t.Run("adjacent registration allowed", func(t *testing.T) {
		fd := mustCreate(t, policy, caller, uffd.UserModeOnly)
		mustRegister(t, fd, caller, base, page, uffd.ModeMissing, mappings)
		mustRegister(t, fd, caller, base+page, page, uffd.ModeMissing, mappings)
		if got := len(fd.Registrations()); got != 2 {
			t.Fatalf("registrations = %d, want 2", got)
		}
	})

	t.Run("unregister splits ranges but refuses holes", func(t *testing.T) {
		fd := mustCreate(t, policy, caller, uffd.UserModeOnly)
		mustRegister(t, fd, caller, base, 4*page, uffd.ModeMissing, mappings)

		if err := fd.UnregisterRange(caller, base+page, 2*page, 0); err != nil {
			t.Fatalf("split unregister: %v", err)
		}
		regs := fd.Registrations()
		if len(regs) != 2 {
			t.Fatalf("registrations after split = %d, want 2", len(regs))
		}
		if regs[0].Start != base || regs[0].End != base+page {
			t.Fatalf("left registration = [%#x,%#x)", regs[0].Start, regs[0].End)
		}
		if regs[1].Start != base+3*page || regs[1].End != base+4*page {
			t.Fatalf("right registration = [%#x,%#x)", regs[1].Start, regs[1].End)
		}

		if err := fd.UnregisterRange(caller, base, 3*page, 0); !errors.Is(err, uffd.ErrNotRegistered) {
			t.Fatalf("unregister across gap error = %v", err)
		}
	})
}

func TestSCMRightsPassingDoesNotGrantRegistrationRights(t *testing.T) {
	policy := uffd.NewAuthorizer(uffd.UserfaultfdEnabled)
	creator := uffd.Caller{
		Process: 100,
		Caps:    uffd.Capabilities{WriteProtect: true},
	}
	receiver := uffd.Caller{
		Process: 200,
		Mappings: []uffd.Mapping{
			anon(0x50000, 0x54000, 200),
		},
	}
	receiverWithWP := receiver
	receiverWithWP.Caps.WriteProtect = true

	const missingBase = uintptr(0x40000)
	const wpBase = uintptr(0x48000)
	mappings := []uffd.Mapping{
		anon(missingBase, missingBase+4*page, creator.Process),
		anon(wpBase, wpBase+4*page, creator.Process),
	}

	fd := mustCreate(t, policy, creator, uffd.UserModeOnly)
	mustRegister(t, fd, creator, missingBase, 2*page, uffd.ModeMissing, mappings)
	mustRegister(t, fd, creator, wpBase, 2*page, uffd.ModeWriteProtect, mappings)

	if err := fd.CopyRange(receiver, missingBase, page, 0); !errors.Is(err, uffd.ErrNotHolder) {
		t.Fatalf("receiver before SCM_RIGHTS error = %v, want ErrNotHolder", err)
	}

	passed, err := fd.Pass(creator, receiver)
	if err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if passed != fd {
		t.Fatal("SCM_RIGHTS must share the same descriptor object")
	}
	if !fd.IsHolder(receiver) {
		t.Fatal("receiver is not recorded as a holder")
	}

	// Already-registered ranges keep their authorization for the receiver.
	if err := fd.CopyRange(receiver, missingBase, page, 0); err != nil {
		t.Fatalf("receiver copy of existing registration: %v", err)
	}
	if err := fd.WakeRange(receiver, missingBase, page, 0); err != nil {
		t.Fatalf("receiver wake of existing registration: %v", err)
	}
	if err := fd.CopyRange(receiver, 0x50000, page, 0); !errors.Is(err, uffd.ErrNotRegistered) {
		t.Fatalf("receiver copy outside registration error = %v, want ErrNotRegistered", err)
	}

	// The receiver cannot register its own memory or extend the existing range.
	err = fd.RegisterRange(receiver, 0x50000, page, 0, uffd.ModeMissing, receiver.Mappings)
	if !errors.Is(err, uffd.ErrNotCreator) {
		t.Fatalf("receiver registering own mapping error = %v, want ErrNotCreator", err)
	}
	err = fd.RegisterRange(receiver, missingBase, 4*page, 0, uffd.ModeMissing, mappings)
	if !errors.Is(err, uffd.ErrNotCreator) {
		t.Fatalf("receiver extending registration error = %v, want ErrNotCreator", err)
	}
	if err := fd.UnregisterRange(receiver, missingBase, page, 0); !errors.Is(err, uffd.ErrNotCreator) {
		t.Fatalf("receiver unregister error = %v, want ErrNotCreator", err)
	}

	// The receiver has no WP grant of its own. The creator's grant must not
	// leak through the descriptor.
	err = fd.WriteProtectRange(receiver, wpBase, page, 0, true)
	if !errors.Is(err, uffd.ErrPermissionDenied) {
		t.Fatalf("receiver unauthorized write protect error = %v, want ErrPermissionDenied", err)
	}

	// An explicit receiver grant permits touching only the already registered
	// WP range, not an arbitrary MISSING range.
	if err := fd.WriteProtectRange(receiverWithWP, wpBase, page, 0, true); err != nil {
		t.Fatalf("receiver with WP grant: %v", err)
	}
	if err := fd.WriteProtectRange(receiverWithWP, missingBase, page, 0, true); !errors.Is(err, uffd.ErrModeMismatch) {
		t.Fatalf("receiver WP on MISSING range error = %v, want ErrModeMismatch", err)
	}

	// Failed attempts did not change the shared registration intervals.
	if got := len(fd.Registrations()); got != 2 {
		t.Fatalf("registrations after receiver attempts = %d, want 2", got)
	}
}

func TestAPIIoctlAndDispatch(t *testing.T) {
	policy := uffd.NewAuthorizer(uffd.UserfaultfdEnabled)
	caller := uffd.Caller{Process: 100}
	fd := mustCreate(t, policy, caller, uffd.UserModeOnly)

	if err := fd.API(caller, uffd.DefaultAPIRequest()); err != nil {
		t.Fatalf("UFFDIO_API: %v", err)
	}
	if !fd.APIReady() {
		t.Fatal("APIReady() = false after handshake")
	}
	if err := fd.API(caller, uffd.APIRequest{Version: 1}); !errors.Is(err, uffd.ErrInvalidAPI) {
		t.Fatalf("bad API version error = %v", err)
	}
	if err := fd.API(caller, uffd.APIRequest{Version: uffd.APIVersion, Features: 1 << 63}); !errors.Is(err, uffd.ErrInvalidAPI) {
		t.Fatalf("bad API features error = %v", err)
	}

	const base = uintptr(0x60000)
	mappings := []uffd.Mapping{anon(base, base+2*page, caller.Process)}

	err := fd.HandleIoctl(caller, uffd.IoctlRequest{
		Kind:     uffd.UFFDIO_REGISTER,
		Start:    base,
		Length:   page,
		Mode:     uffd.ModeMissing,
		Mappings: mappings,
	})
	if err != nil {
		t.Fatalf("dispatched register: %v", err)
	}
	if err := fd.HandleIoctl(caller, uffd.IoctlRequest{
		Kind:   uffd.UFFDIO_COPY,
		Start:  base,
		Length: page,
	}); err != nil {
		t.Fatalf("dispatched copy: %v", err)
	}
	if err := fd.HandleIoctl(caller, uffd.IoctlRequest{
		Kind:   uffd.UFFDIO_WAKE,
		Start:  base,
		Length: page,
	}); err != nil {
		t.Fatalf("dispatched wake: %v", err)
	}
	if err := fd.HandleIoctl(caller, uffd.IoctlRequest{Kind: uffd.IoctlKind(999)}); !errors.Is(err, uffd.ErrUnknownIoctl) {
		t.Fatalf("unknown ioctl error = %v", err)
	}
}
