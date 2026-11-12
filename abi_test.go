package netsec

import (
	"errors"
	"testing"
)

func TestSupportedAccessFSCumulative(t *testing.T) {
	cases := []struct {
		abi       ABI
		first     AccessFS
		added     AccessFS
		supersets []ABI
	}{
		{ABI1, AccessFSExecute, 0, nil},
		{ABI2, AccessFSExecute, AccessFSRefer, []ABI{ABI1}},
		{ABI3, AccessFSRefer, AccessFSTruncate, []ABI{ABI2}},
		{ABI4, AccessFSTruncate, 0, []ABI{ABI3}},
		{ABI5, AccessFSTruncate, 0, []ABI{ABI4}},
		{ABI6, AccessFSTruncate, AccessFSIoctlDev, []ABI{ABI5}},
	}

	for _, tc := range cases {
		mask := tc.abi.SupportedAccessFS()
		if !mask.Contains(tc.first) {
			t.Errorf("%s mask %s missing baseline right %s", tc.abi, mask, tc.first)
		}
		if tc.added != 0 && !mask.Contains(tc.added) {
			t.Errorf("%s mask %s missing new right %s", tc.abi, mask, tc.added)
		}
		for _, older := range tc.supersets {
			oldMask := older.SupportedAccessFS()
			if !mask.Contains(oldMask) {
				t.Errorf("%s mask %s is not a superset of %s mask %s",
					tc.abi, mask, older, oldMask)
			}
		}
	}

	if ABI(42).SupportedAccessFS() != 0 {
		t.Fatalf("unknown ABI should report no supported rights")
	}
}

func TestAccessFSStringAndContains(t *testing.T) {
	if got := (AccessFSReadFile | AccessFSWriteFile).String(); got != "WRITE_FILE|READ_FILE" {
		t.Fatalf("String() = %q", got)
	}
	if got := AccessFS(0).String(); got != "0" {
		t.Fatalf("zero mask String() = %q", got)
	}
	if got := AccessFS(1 << 63).String(); got != "0x8000000000000000" {
		t.Fatalf("unknown bit String() = %q", got)
	}
	mask := AccessFSReadFile | AccessFSReadDir
	if !mask.Contains(AccessFSReadFile) || mask.Contains(AccessFSWriteFile) {
		t.Fatalf("Contains semantics wrong for %s", mask)
	}
}

func TestComposeRejectsRightsUnknownToABI(t *testing.T) {
	tests := []struct {
		name      string
		abi       ABI
		layer     Layer
		wantUnsup AccessFS
	}{
		{
			name: "abi1 refer in handled mask",
			abi:  ABI1,
			layer: Layer{
				Handled: AccessFSReadFile | AccessFSRefer,
				Rules: []PathRule{
					{Path: "/tmp", Dir: true, Allowed: AccessFSReadFile},
				},
			},
			wantUnsup: AccessFSRefer,
		},
		{
			name:      "abi1 truncate in handled mask",
			abi:       ABI1,
			layer:     Layer{Handled: AccessFSWriteFile | AccessFSTruncate},
			wantUnsup: AccessFSTruncate,
		},
		{
			name: "abi1 refer and truncate reported together",
			abi:  ABI1,
			layer: Layer{
				Handled: AccessFSReadFile | AccessFSRefer | AccessFSTruncate,
			},
			wantUnsup: AccessFSRefer | AccessFSTruncate,
		},
		{
			name:      "abi2 accepts refer but rejects truncate",
			abi:       ABI2,
			layer:     Layer{Handled: AccessFSTruncate},
			wantUnsup: AccessFSTruncate,
		},
		{
			name:      "abi3 rejects ioctl dev",
			abi:       ABI3,
			layer:     Layer{Handled: AccessFSIoctlDev},
			wantUnsup: AccessFSIoctlDev,
		},
		{
			name: "abi1 truncate granted by path rule",
			abi:  ABI1,
			layer: Layer{
				Handled: AccessFSWriteFile,
				Rules: []PathRule{
					{Path: "/var/log/app.log", Allowed: AccessFSTruncate},
				},
			},
			wantUnsup: AccessFSTruncate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compose(tt.abi, []Layer{tt.layer})
			var unsupported *UnsupportedAccessError
			if !errors.As(err, &unsupported) {
				t.Fatalf("Compose() error = %v, want *UnsupportedAccessError", err)
			}
			if unsupported.Unsupported != tt.wantUnsup {
				t.Fatalf("Unsupported = %s, want %s",
					unsupported.Unsupported, tt.wantUnsup)
			}
			if unsupported.Supported != tt.abi.SupportedAccessFS() {
				t.Fatalf("Supported = %s, want %s",
					unsupported.Supported, tt.abi.SupportedAccessFS())
			}
			if unsupported.Unsupported&unsupported.Supported != 0 {
				t.Fatalf("unsupported bits %s overlap supported mask",
					unsupported.Unsupported)
			}
		})
	}
}

func TestReferCompanionRightsEnforced(t *testing.T) {
	_, err := Compose(ABI2, []Layer{
		{
			Handled: AccessFSRefer | AccessFSRemoveDir | AccessFSRemoveFile |
				AccessFSMakeDir,
		},
	})
	var invalid *InvalidConfigError
	if !errors.As(err, &invalid) {
		t.Fatalf("Compose() error = %v, want *InvalidConfigError", err)
	}

	full := AccessFSRefer | referCompanionRights
	if _, err := Compose(ABI2, []Layer{
		{Handled: full, Rules: []PathRule{{Path: "/", Dir: true, Allowed: full}}},
	}); err != nil {
		t.Fatalf("valid REFER ruleset rejected: %v", err)
	}
}

func TestConfigurationValidation(t *testing.T) {
	t.Run("unknown abi", func(t *testing.T) {
		_, err := Compose(ABI(99), []Layer{{Handled: AccessFSReadFile}})
		var invalid *InvalidConfigError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *InvalidConfigError", err)
		}
	})

	t.Run("empty handled mask", func(t *testing.T) {
		_, err := Compose(ABI1, []Layer{{Handled: 0}})
		var invalid *InvalidConfigError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *InvalidConfigError", err)
		}
	})

	t.Run("rule grants unhandled right", func(t *testing.T) {
		_, err := Compose(ABI1, []Layer{
			{
				Handled: AccessFSReadFile,
				Rules:   []PathRule{{Path: "/a", Allowed: AccessFSWriteFile}},
			},
		})
		var invalid *InvalidConfigError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *InvalidConfigError", err)
		}
	})

	t.Run("relative and empty paths rejected", func(t *testing.T) {
		for _, bad := range []string{"", "relative/path"} {
			_, err := Compose(ABI1, []Layer{
				{
					Handled: AccessFSReadFile,
					Rules:   []PathRule{{Path: bad, Allowed: AccessFSReadFile}},
				},
			})
			var invalid *InvalidConfigError
			if !errors.As(err, &invalid) {
				t.Fatalf("path %q error = %v, want *InvalidConfigError", bad, err)
			}
		}
	})

	t.Run("paths are cleaned", func(t *testing.T) {
		profile, err := Compose(ABI1, []Layer{
			{
				Handled: AccessFSReadFile,
				Rules: []PathRule{
					{Path: "/srv/conf/../data", Dir: true, Allowed: AccessFSReadFile},
				},
			},
		})
		if err != nil {
			t.Fatalf("Compose() error = %v", err)
		}
		eff, err := profile.EffectiveAccess("/srv/data/file")
		if err != nil {
			t.Fatalf("EffectiveAccess() error = %v", err)
		}
		if !eff.Allows(AccessFSReadFile) {
			t.Fatalf("cleaned directory rule did not match: %+v", eff)
		}
	})
}
