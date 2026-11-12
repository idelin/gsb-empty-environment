// Package netsec models the policy decisions an unprivileged container
// runtime must make when simulating overlayfs mounts and copy-up.
//
// The model mirrors a hostile flow: an attacker mounts an overlay inside a
// user namespace, forces a lower file to be copied up to the writable upper
// layer, and then expects the copied file to retain a root owner, a setuid
// bit, or file capabilities that become meaningful again in the host
// namespace. The checks here are pure policy; they touch no filesystem.
package netsec

import (
	"io/fs"
	"path"
	"strings"
)

// OverflowID is the overflow uid/gid (nobody/nogroup) that the kernel shows
// for an id which has no entry in the mount's user namespace id mappings.
const OverflowID uint32 = 65534

// UIDMap is one line of a user namespace uid_map / gid_map:
//
//	containerID (count IDs) <=> hostID (count IDs)
//
// A lower inode's stored owner id lives in the host (initial) namespace, so
// HostID is the id as written on the lower filesystem and ContainerID is the
// id it appears as inside the mounting user namespace.
type UIDMap struct {
	ContainerID uint32
	HostID      uint32
	Count       uint32
}

// ContainsHost reports whether hostID falls inside this mapping line.
func (m UIDMap) ContainsHost(hostID uint32) bool {
	return hostID >= m.HostID && hostID < m.HostID+m.Count
}

// MapToHost translates an id expressed inside the mounting user namespace to
// the host id it is backed by. Unmapped ids resolve to OverflowID.
func MapToHost(maps []UIDMap, containerID uint32) uint32 {
	for _, m := range maps {
		if containerID >= m.ContainerID && containerID < m.ContainerID+m.Count {
			return m.HostID + (containerID - m.ContainerID)
		}
	}
	return OverflowID
}

// mapFromHost returns the namespace view of a lower inode's host owner id.
// Unmapped ids resolve to OverflowID, matching the kernel's overflow id.
func mapFromHost(maps []UIDMap, hostID uint32) uint32 {
	for _, m := range maps {
		if m.ContainsHost(hostID) {
			return m.ContainerID + (hostID - m.HostID)
		}
	}
	return OverflowID
}

// LowerEntry describes one lower directory entry that may be copied up.
// UID and GID are the owner ids stored on the lower filesystem (host
// namespace view). Mode carries the permission bits, including setuid and
// setgid. FileCaps is the raw security.capability xattr; nil/empty means the
// entry carries no file capabilities.
// LowerEntry describes one lower directory entry that may be copied up.
// UID and GID are the owner ids stored on the lower filesystem, i.e. the
// host (initial) namespace view. The host-root ownership guard therefore
// compares UID directly against zero. Mode carries the permission bits,
// including setuid and setgid. FileCaps is the raw security.capability
// xattr; nil/empty means the entry carries no file capabilities.
type LowerEntry struct {
	Path     string
	UID      uint32
	GID      uint32
	Mode     fs.FileMode
	FileCaps []byte
}

// MountOptions mirrors the overlay mount flags relevant to the policy.
type MountOptions struct {
	MetaCopy    bool
	RedirectDir bool
	UserXattr   bool
	NoSUID      bool
}

// ModifyOp describes the change that triggers a copy-up.
type ModifyOp struct {
	// MetadataOnly is true when only metadata (and not data) is written.
	// With metacopy this yields a metadata-only copy-up.
	MetadataOnly bool

	// RedirectTarget is the redirect xattr target of a metadata-only
	// copy-up, expressed relative to one of the mount's lower roots.
	RedirectTarget string
}

// UpperEntry is the projected upper inode produced by an allowed copy-up.
// UpperEntry is the projected upper inode produced by an allowed copy-up.
// UID and GID are the owner ids as visible inside the mounting user
// namespace (OverflowID when unmapped); the host-root guard acts on the
// lower entry's stored host uid before this projection is built.
type UpperEntry struct {
	Path            string
	UID             uint32
	GID             uint32
	Mode            fs.FileMode
	FileCapsCleared bool
	MetadataOnly    bool
	RedirectXattr   string
	RedirectTarget  string
	SetuidEffective bool
	SetgidEffective bool
}

// Decision is the result of evaluating a copy-up against the policy.
type Decision struct {
	Allowed bool
	Reason  string
	Detail  string
	Upper   *UpperEntry
}

// Reason codes returned on a denied copy-up.
const (
	ReasonInvalidMount        = "invalid_mount"
	ReasonHostRootOwner       = "host_root_owner"
	ReasonFileCapabilities    = "file_capabilities"
	ReasonRedirectOutOfScope  = "redirect_out_of_scope"
	ReasonMetaCopyUnavailable = "metacopy_unavailable"
)

func deny(reason, detail string) Decision {
	return Decision{Allowed: false, Reason: reason, Detail: detail}
}

func allow(upper *UpperEntry) Decision {
	return Decision{Allowed: true, Upper: upper}
}

// Mount binds the id mappings, options and lower roots that define one
// simulated overlay mount.
type Mount struct {
	UIDMaps []UIDMap
	GIDMaps []UIDMap
	Options MountOptions

	// LowerRoots are the absolute lower directories of the overlay mount.
	LowerRoots []string

	// StripFileCaps permits a copy-up to drop security.capability instead
	// of rejecting the entry. When false (the secure default), any lower
	// entry carrying file capabilities is denied.
	StripFileCaps bool
}

// Validate checks the mount configuration itself. metacopy depends on
// redirect_dir, and at least one lower root must be present for redirect
// containment to be meaningful.
func (mnt Mount) Validate() Decision {
	if len(mnt.LowerRoots) == 0 {
		return deny(ReasonInvalidMount, "overlay mount requires at least one lower root")
	}
	if mnt.Options.MetaCopy && !mnt.Options.RedirectDir {
		return deny(ReasonInvalidMount, "metacopy requires redirect_dir to be enabled")
	}
	for _, m := range mnt.UIDMaps {
		if m.Count == 0 {
			return deny(ReasonInvalidMount, "uid mapping line must cover at least one id")
		}
	}
	for _, m := range mnt.GIDMaps {
		if m.Count == 0 {
			return deny(ReasonInvalidMount, "gid mapping line must cover at least one id")
		}
	}
	return Decision{Allowed: true}
}

// CopyUp evaluates copying entry up to the writable layer in response to op.
func (mnt Mount) CopyUp(entry LowerEntry, op ModifyOp) Decision {
	if d := mnt.Validate(); !d.Allowed {
		return d
	}

	// The host-root guard acts on the id stored on the lower filesystem, not
	// on its remapped namespace view: a host-root owned lower inode would be
	// copied up as root in the host namespace regardless of how it is mapped.
	if entry.UID == 0 {
		return deny(ReasonHostRootOwner,
			"copy-up would write host uid 0 as the upper owner of "+entry.Path)
	}

	// Owner mapping: unmapped ids surface as the overflow id in the upper view.
	upperUID := mapFromHost(mnt.UIDMaps, entry.UID)
	upperGID := mapFromHost(mnt.GIDMaps, entry.GID)

	// File capabilities survive a plain copy-up and stay executable in the
	// host namespace, so they must be stripped explicitly or refused.
	capsCleared := false
	if len(entry.FileCaps) > 0 {
		if !mnt.StripFileCaps {
			return deny(ReasonFileCapabilities,
				"lower entry "+entry.Path+" carries security.capability and stripping is disabled")
		}
		capsCleared = true
	}

	// A metadata-only copy-up exists only with metacopy + redirect_dir.
	metadataOnly := op.MetadataOnly && mnt.Options.MetaCopy
	if op.MetadataOnly && !metadataOnly {
		return deny(ReasonMetaCopyUnavailable,
			"metadata-only copy-up requires metacopy and redirect_dir")
	}

	upper := &UpperEntry{
		Path:            entry.Path,
		UID:             upperUID,
		GID:             upperGID,
		Mode:            entry.Mode,
		FileCapsCleared: capsCleared,
		MetadataOnly:    metadataOnly,
	}

	// The redirect must stay inside a lower root; otherwise it can point the
	// upper dentry at an arbitrary host path outside the mount.
	if metadataOnly && op.RedirectTarget != "" {
		root, ok := mnt.resolveInLower(op.RedirectTarget)
		if !ok {
			return deny(ReasonRedirectOutOfScope,
				"redirect target "+op.RedirectTarget+" escapes every lower root")
		}
		upper.RedirectXattr = mnt.redirectXattrName()
		upper.RedirectTarget = path.Join(root, cleanRel(op.RedirectTarget))
	}

	// The setuid/setgid bits are stored on the mode but have no effect on a
	// nosuid mount.
	upper.SetuidEffective = entry.Mode&fs.ModeSetuid != 0 && !mnt.Options.NoSUID
	upper.SetgidEffective = entry.Mode&fs.ModeSetgid != 0 && !mnt.Options.NoSUID

	return allow(upper)
}

// redirectXattrName returns the xattr used to store redirects. With userxattr
// overlay uses the user.* namespace, otherwise the trusted.* namespace.
func (mnt Mount) redirectXattrName() string {
	if mnt.Options.UserXattr {
		return "user.overlay.redirect"
	}
	return "trusted.overlay.redirect"
}

// resolveInLower reports whether target (relative to a lower root) remains
// inside one of the mount's lower roots after lexical cleaning.
func (mnt Mount) resolveInLower(target string) (string, bool) {
	cleaned := cleanRel(target)
	if cleaned == "" {
		return "", false
	}
	for _, root := range mnt.LowerRoots {
		base := path.Clean(root)
		candidate := path.Join(base, cleaned)
		if candidate == base || strings.HasPrefix(candidate, base+"/") {
			return base, true
		}
	}
	return "", false
}

// cleanRel lexically cleans target and rejects absolute paths and traversal
// that escapes the relative root (leading "..").
func cleanRel(target string) string {
	cleaned := path.Clean(target)
	if path.IsAbs(cleaned) {
		return ""
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	if cleaned == "." {
		return ""
	}
	return cleaned
}
