package netsec

import (
	"io/fs"
	"testing"
)

const lowerRoot = "/var/lib/container/lower"

func baseMount() Mount {
	return Mount{
		// Namespace uid 1000 is backed by host uid 0; namespace 0..999
		// maps to host 1000..1999.
		UIDMaps: []UIDMap{
			{ContainerID: 1000, HostID: 0, Count: 1},
			{ContainerID: 0, HostID: 1000, Count: 1000},
		},
		GIDMaps: []UIDMap{
			{ContainerID: 0, HostID: 1000, Count: 1000},
		},
		Options: MountOptions{
			MetaCopy:    true,
			RedirectDir: true,
			UserXattr:   true,
		},
		LowerRoots: []string{lowerRoot},
	}
}

func setuidFile() fs.FileMode {
	return fs.ModeSetuid | 0o755
}

// A lower inode owned by host uid 0 must never be copied up as host root,
// even though that id has a mapping in the mounting user namespace.
func TestCopyUpRejectsHostRootOwner(t *testing.T) {
	mnt := baseMount()
	if host := MapToHost(mnt.UIDMaps, 1000); host != 0 {
		t.Fatalf("namespace uid 1000 must be backed by host uid 0, got %d", host)
	}

	entry := LowerEntry{
		Path: "/bin/su",
		UID:  0, // host uid 0, visible as namespace uid 1000
		GID:  1000,
		Mode: setuidFile(),
	}

	d := mnt.CopyUp(entry, ModifyOp{})
	if d.Allowed {
		t.Fatalf("expected denial for host-root owner, got allowed upper %+v", d.Upper)
	}
	if d.Reason != ReasonHostRootOwner {
		t.Fatalf("expected reason %q, got %q", ReasonHostRootOwner, d.Reason)
	}

	// Control: a host uid 1000 inode shows up as namespace uid 0. The guard
	// must key on the host id, so this non-root owner is allowed.
	control := LowerEntry{Path: "/bin/ok", UID: 1000, GID: 1000, Mode: 0o755}
	cd := mnt.CopyUp(control, ModifyOp{})
	if !cd.Allowed {
		t.Fatalf("host uid 1000 (namespace uid 0) must be allowed, got %s: %s", cd.Reason, cd.Detail)
	}
	if cd.Upper.UID != 0 {
		t.Fatalf("expected namespace uid 0 view, got %d", cd.Upper.UID)
	}
}

// Unmapped ids surface as the overflow id and a normal (non-root) owner is
// copied up successfully.
func TestCopyUpUnmappedIDBecomesOverflow(t *testing.T) {
	mnt := baseMount()
	entry := LowerEntry{
		Path: "/etc/hosts",
		UID:  5000, // outside every uid mapping
		GID:  7000, // outside every gid mapping
		Mode: 0o644,
	}

	d := mnt.CopyUp(entry, ModifyOp{})
	if !d.Allowed {
		t.Fatalf("expected allow, got denial %s: %s", d.Reason, d.Detail)
	}
	if d.Upper.UID != OverflowID {
		t.Fatalf("expected overflow uid %d, got %d", OverflowID, d.Upper.UID)
	}
	if d.Upper.GID != OverflowID {
		t.Fatalf("expected overflow gid %d, got %d", OverflowID, d.Upper.GID)
	}
}

// File capabilities must be refused by default and only carried up when the
// mount explicitly opts in to stripping them.
func TestCopyUpFileCapabilities(t *testing.T) {
	entry := LowerEntry{
		Path:     "/usr/bin/ping",
		UID:      1000,
		GID:      1000,
		Mode:     0o755,
		FileCaps: []byte{0x01, 0x00, 0x00, 0x02},
	}

	mnt := baseMount()
	if d := mnt.CopyUp(entry, ModifyOp{}); d.Allowed {
		t.Fatalf("capable file must be denied when stripping is off, got %+v", d.Upper)
	} else if d.Reason != ReasonFileCapabilities {
		t.Fatalf("expected reason %q, got %q", ReasonFileCapabilities, d.Reason)
	}

	mnt.StripFileCaps = true
	d := mnt.CopyUp(entry, ModifyOp{})
	if !d.Allowed {
		t.Fatalf("expected allow with stripping enabled, got %s: %s", d.Reason, d.Detail)
	}
	if !d.Upper.FileCapsCleared {
		t.Fatalf("expected FileCapsCleared to be true")
	}
}

// metacopy metadata-only copy-ups must keep their redirect inside a lower
// root. In-scope redirects resolve to an absolute lower path; traversal and
// absolute escapes are rejected.
func TestCopyUpRedirectContainment(t *testing.T) {
	entry := LowerEntry{
		Path: "/data/link",
		UID:  1000,
		GID:  1000,
		Mode: 0o644,
	}

	mnt := baseMount()

	t.Run("in_scope", func(t *testing.T) {
		d := mnt.CopyUp(entry, ModifyOp{
			MetadataOnly:   true,
			RedirectTarget: "etc/config",
		})
		if !d.Allowed {
			t.Fatalf("expected allow, got %s: %s", d.Reason, d.Detail)
		}
		if !d.Upper.MetadataOnly {
			t.Fatalf("expected metadata-only upper entry")
		}
		if d.Upper.RedirectXattr != "user.overlay.redirect" {
			t.Fatalf("expected userxattr redirect name, got %q", d.Upper.RedirectXattr)
		}
		if d.Upper.RedirectTarget != lowerRoot+"/etc/config" {
			t.Fatalf("unexpected resolved redirect %q", d.Upper.RedirectTarget)
		}
	})

	t.Run("out_of_scope_traversal", func(t *testing.T) {
		d := mnt.CopyUp(entry, ModifyOp{
			MetadataOnly:   true,
			RedirectTarget: "../../etc/shadow",
		})
		if d.Allowed {
			t.Fatalf("expected traversal redirect to be denied, got %+v", d.Upper)
		}
		if d.Reason != ReasonRedirectOutOfScope {
			t.Fatalf("expected reason %q, got %q", ReasonRedirectOutOfScope, d.Reason)
		}
	})

	t.Run("out_of_scope_absolute", func(t *testing.T) {
		d := mnt.CopyUp(entry, ModifyOp{
			MetadataOnly:   true,
			RedirectTarget: "/etc/shadow",
		})
		if d.Allowed {
			t.Fatalf("expected absolute redirect to be denied, got %+v", d.Upper)
		}
		if d.Reason != ReasonRedirectOutOfScope {
			t.Fatalf("expected reason %q, got %q", ReasonRedirectOutOfScope, d.Reason)
		}
	})
}

// On a nosuid mount the setuid/setgid bits remain stored in the mode but must
// not be reported as effective.
func TestCopyUpNoSUID(t *testing.T) {
	entry := LowerEntry{
		Path: "/bin/tool",
		UID:  1000,
		GID:  1000,
		Mode: fs.ModeSetuid | fs.ModeSetgid | 0o755,
	}

	mnt := baseMount()
	mnt.Options.NoSUID = true
	d := mnt.CopyUp(entry, ModifyOp{})
	if !d.Allowed {
		t.Fatalf("expected allow, got %s: %s", d.Reason, d.Detail)
	}
	if d.Upper.SetuidEffective || d.Upper.SetgidEffective {
		t.Fatalf("setuid/setgid must be ineffective under nosuid, got %+v", d.Upper)
	}
	if d.Upper.Mode&fs.ModeSetuid == 0 {
		t.Fatalf("setuid bit must remain stored in the upper mode")
	}

	mnt.Options.NoSUID = false
	d = mnt.CopyUp(entry, ModifyOp{})
	if !d.Allowed || !d.Upper.SetuidEffective || !d.Upper.SetgidEffective {
		t.Fatalf("setuid/setgid should be effective without nosuid, got %+v", d.Upper)
	}
}

// metacopy cannot be enabled without redirect_dir, and a metadata-only
// copy-up without those flags is refused.
func TestMountValidation(t *testing.T) {
	mnt := baseMount()
	mnt.Options.MetaCopy = true
	mnt.Options.RedirectDir = false
	if d := mnt.Validate(); d.Allowed || d.Reason != ReasonInvalidMount {
		t.Fatalf("metacopy without redirect_dir must be invalid, got %+v", d)
	}

	mnt = baseMount()
	mnt.Options.MetaCopy = false
	mnt.Options.RedirectDir = false
	entry := LowerEntry{Path: "/x", UID: 1000, GID: 1000, Mode: 0o644}
	d := mnt.CopyUp(entry, ModifyOp{MetadataOnly: true})
	if d.Allowed || d.Reason != ReasonMetaCopyUnavailable {
		t.Fatalf("metadata-only copy-up needs metacopy, got %+v", d)
	}

	mnt = baseMount()
	mnt.LowerRoots = nil
	if d := mnt.Validate(); d.Allowed || d.Reason != ReasonInvalidMount {
		t.Fatalf("mount without lower roots must be invalid, got %+v", d)
	}
}

// Without userxattr, redirects live in the trusted.* namespace.
func TestRedirectXattrNamespace(t *testing.T) {
	mnt := baseMount()
	mnt.Options.UserXattr = false
	entry := LowerEntry{Path: "/d/link", UID: 1000, GID: 1000, Mode: 0o644}
	d := mnt.CopyUp(entry, ModifyOp{MetadataOnly: true, RedirectTarget: "etc/hosts"})
	if !d.Allowed {
		t.Fatalf("expected allow, got %s: %s", d.Reason, d.Detail)
	}
	if d.Upper.RedirectXattr != "trusted.overlay.redirect" {
		t.Fatalf("expected trusted xattr, got %q", d.Upper.RedirectXattr)
	}
}

func TestMapToHostAndOverflow(t *testing.T) {
	maps := []UIDMap{{ContainerID: 1000, HostID: 0, Count: 1}}
	if got := MapToHost(maps, 1000); got != 0 {
		t.Fatalf("container 1000 should map to host 0, got %d", got)
	}
	if got := MapToHost(maps, 42); got != OverflowID {
		t.Fatalf("unmapped id should be overflow, got %d", got)
	}
}
