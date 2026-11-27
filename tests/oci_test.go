package oci_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"testing"

	oci "formal.local/w27-7184be2212"
)

func capPayload(t *testing.T, revision uint32, effective bool, rootID uint32, permitted, inheritable uint64) []byte {
	t.Helper()
	set := &oci.CapabilitySet{
		Revision:      revision,
		Effective:     effective,
		Permitted:     permitted,
		Inheritable:   inheritable,
		RootID:        rootID,
		RootIDPresent: revision == oci.VFSCapabilityRevision3,
	}
	payload, err := oci.EncodeCapability(set)
	if err != nil {
		t.Fatalf("EncodeCapability: %v", err)
	}
	return payload
}

func TestDecodeCapabilityV2AndV3(t *testing.T) {
	payload := capPayload(
		t,
		oci.VFSCapabilityRevision2,
		true,
		0,
		1<<oci.CAP_SYS_ADMIN|1<<oci.CAP_NET_RAW,
		1<<oci.CAP_SETUID,
	)
	if len(payload) != 20 {
		t.Fatalf("v2 payload length = %d, want 20", len(payload))
	}

	caps, err := oci.DecodeCapability(payload)
	if err != nil {
		t.Fatalf("DecodeCapability v2: %v", err)
	}
	if caps.Revision != oci.VFSCapabilityRevision2 || caps.Version != oci.VFSCapabilityRevision2 {
		t.Fatalf("unexpected revision 0x%08x", caps.Revision)
	}
	if !caps.Effective {
		t.Fatal("VFS_CAP_FLAGS_EFFECTIVE was not decoded")
	}
	if !caps.Has(oci.CAP_SYS_ADMIN) || !caps.Has(oci.CAP_NET_RAW) {
		t.Fatalf("permitted masks missing dangerous caps: %#x", caps.Permitted)
	}
	if caps.Has(oci.CAP_SETUID) {
		t.Fatal("CAP_SETUID should only be in inheritable")
	}
	if !caps.HasInheritable(oci.CAP_SETUID) {
		t.Fatalf("inheritable mask not decoded: %#x", caps.Inheritable)
	}
	if caps.RootIDPresent {
		t.Fatal("v2 payload unexpectedly has rootid")
	}

	v3 := capPayload(t, oci.VFSCapabilityRevision3, false, 1234, 1<<oci.CAP_SETUID, 0)
	if len(v3) != 24 {
		t.Fatalf("v3 payload length = %d, want 24", len(v3))
	}
	decodedV3, err := oci.DecodeCapability(v3)
	if err != nil {
		t.Fatalf("DecodeCapability v3: %v", err)
	}
	if !decodedV3.RootIDPresent || decodedV3.RootID != 1234 {
		t.Fatalf("v3 rootid = %d present=%v, want 1234", decodedV3.RootID, decodedV3.RootIDPresent)
	}
	if decodedV3.Effective {
		t.Fatal("effective flag unexpectedly set")
	}

	bad := append([]byte(nil), payload...)
	bad[0] = 0
	bad[3] = 0x04 // unknown revision 0x04000000
	if _, err := oci.DecodeCapability(bad); !errors.Is(err, oci.ErrCapabilityRevision) {
		t.Fatalf("unknown revision error = %v, want ErrCapabilityRevision", err)
	}
	if _, err := oci.DecodeCapability(payload[:18]); !errors.Is(err, oci.ErrCapabilityLength) {
		t.Fatalf("truncated length error = %v, want ErrCapabilityLength", err)
	}
}

func TestWhiteoutOpaqueOrderAndCredentialResidue(t *testing.T) {
	layers := []oci.Layer{
		{Entries: []oci.Entry{
			{Path: "etc", Type: oci.TypeDirectory, Mode: 0o755},
			{Path: "etc/shadow", Mode: 0o600, Content: []byte("root:$6$hash:::::::\n")},
			{Path: "etc/old.conf", Mode: 0o644, Content: []byte("old")},
			{Path: "usr/bin", Type: oci.TypeDirectory, Mode: 0o755},
			{Path: "usr/bin/evil", Mode: 0o4755, UID: 0},
		}},
		{Entries: []oci.Entry{
			{Path: "etc/.wh..wh..opq"},
			{Path: "etc/hosts", Mode: 0o644, Content: []byte("127.0.0.1 localhost\n")},
			{Path: "etc/new.conf", Mode: 0o644, Content: []byte("new")},
			{Path: "usr/bin/.wh.evil"},
		}},
		{Entries: []oci.Entry{
			{Path: "etc/.wh.hosts"},
			{Path: "etc/issue", Mode: 0o644, Content: []byte("welcome")},
		}},
	}

	merged, err := oci.MergeWithSnapshots(layers...)
	if err != nil {
		t.Fatalf("MergeWithSnapshots: %v", err)
	}
	fs := merged.Final

	if _, err := fs.Stat("etc/shadow"); err == nil {
		t.Fatal("etc/shadow survived opaque whiteout")
	}
	if _, err := fs.Stat("etc/old.conf"); err == nil {
		t.Fatal("etc/old.conf survived opaque whiteout")
	}
	if _, err := fs.Stat("etc/hosts"); err == nil {
		t.Fatal("etc/hosts survived ordinary whiteout in later layer")
	}
	if _, err := fs.Stat("etc/new.conf"); err != nil {
		t.Fatalf("same-layer entry after opaque marker was removed: %v", err)
	}
	if _, err := fs.Stat("etc/issue"); err != nil {
		t.Fatalf("later-layer entry missing: %v", err)
	}
	if _, err := fs.Stat("usr/bin/evil"); err == nil {
		t.Fatal("ordinary whiteout did not remove usr/bin/evil")
	}

	snap0 := merged.Snapshots[0]
	if _, err := snap0.Stat("etc/shadow"); err != nil {
		t.Fatalf("first snapshot lost lower credential: %v", err)
	}
	if _, err := snap0.Stat("usr/bin/evil"); err != nil {
		t.Fatalf("first snapshot lost setuid binary: %v", err)
	}
	snap1 := merged.Snapshots[1]
	if _, err := snap1.Stat("etc/hosts"); err != nil {
		t.Fatalf("second snapshot should contain hosts: %v", err)
	}
	if _, err := snap1.Stat("etc/shadow"); err == nil {
		t.Fatal("second snapshot should have opaque-hidden shadow")
	}

	report, err := oci.Validate(fs)
	if err != nil {
		t.Fatalf("Validate final: %v", err)
	}
	cred := report.OfType(oci.FindingCredentialResidue)
	if len(cred) != 1 || cred[0].Path != "etc/shadow" {
		t.Fatalf("credential residue findings = %#v", cred)
	}
	if !cred[0].Hidden || cred[0].CredentialKind == "" {
		t.Fatalf("credential finding missing hidden/kind: %#v", cred[0])
	}

	all, _, err := oci.VerifyLayers(layers)
	if err != nil {
		t.Fatalf("VerifyLayers: %v", err)
	}
	var foundSnapshotSUID bool
	for _, finding := range all.OfType(oci.FindingSUID) {
		if finding.Path == "usr/bin/evil" && finding.SnapshotIndex == 0 {
			foundSnapshotSUID = true
		}
	}
	if !foundSnapshotSUID {
		t.Fatalf("intermediate SUID finding missing: %#v", all.Findings)
	}
}

func TestInvalidWhiteoutNamesRejected(t *testing.T) {
	for _, name := range []string{".wh..", ".wh...", ".wh.", "dir/.wh..", ".wh.foo/bar", "dir/.wh.../etc"} {
		t.Run(name, func(t *testing.T) {
			_, err := oci.Merge(oci.Layer{Entries: []oci.Entry{{Path: name}}})
			if err == nil {
				t.Fatal("expected invalid whiteout error")
			}
			if !errors.Is(err, oci.ErrInvalidWhiteout) {
				t.Fatalf("error %v does not wrap ErrInvalidWhiteout", err)
			}
		})
	}
}

func TestSUIDAndSGIDWhitelist(t *testing.T) {
	fs, err := oci.Merge(oci.Layer{Entries: []oci.Entry{
		{Path: "usr/bin/safe-suid", Mode: 0o4755, UID: 0},
		{Path: "usr/bin/evil-suid", Mode: 0o4755, UID: 1000},
		{Path: "usr/bin/safe-sgid", Mode: 0o2755, GID: 0},
		{Path: "usr/bin/evil-sgid", Mode: 0o2755, GID: 1000},
	}})
	if err != nil {
		t.Fatal(err)
	}

	policy := oci.DefaultPolicy()
	policy.AllowedSUID = []string{"/usr/bin/safe-suid"}
	policy.AllowedSGID = []string{"usr/bin/safe-sgid"}
	report, err := oci.Validate(fs, policy)
	if err != nil {
		t.Fatal(err)
	}

	suid := report.OfType(oci.FindingSUID)
	if len(suid) != 1 || suid[0].Path != "usr/bin/evil-suid" {
		t.Fatalf("SUID findings = %#v", suid)
	}
	sgid := report.OfType(oci.FindingSGID)
	if len(sgid) != 1 || sgid[0].Path != "usr/bin/evil-sgid" {
		t.Fatalf("SGID findings = %#v", sgid)
	}
	if suid[0].Severity != oci.SeverityHigh {
		t.Fatalf("non-root SUID severity = %q, want high", suid[0].Severity)
	}
}

func TestDangerousCapabilityPolicyAndWhitelist(t *testing.T) {
	payload := capPayload(t, oci.VFSCapabilityRevision3, true, 0,
		1<<oci.CAP_SYS_ADMIN|1<<oci.CAP_NET_RAW, 0)
	fs, err := oci.Merge(oci.Layer{Entries: []oci.Entry{
		{
			Path:   "usr/bin/cap-evil",
			Mode:   0o755,
			Xattrs: map[string][]byte{oci.SecurityCapabilityXattr: payload},
		},
		{Path: "usr/bin/bad-cap", Mode: 0o755, Xattrs: map[string][]byte{
			oci.SecurityCapabilityXattr: []byte("not a capability payload"),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	report, err := oci.Validate(fs)
	if err != nil {
		t.Fatal(err)
	}
	capFindings := report.OfType(oci.FindingDangerousCapability)
	if len(capFindings) != 1 || capFindings[0].Path != "usr/bin/cap-evil" {
		t.Fatalf("capability findings = %#v", capFindings)
	}
	if len(capFindings[0].Capabilities) != 2 {
		t.Fatalf("capability names = %v", capFindings[0].Capabilities)
	}
	if !report.HasType(oci.FindingInvalidCapability) {
		t.Fatalf("malformed xattr not reported: %#v", report.Findings)
	}

	policy := oci.DefaultPolicy()
	policy.Capabilities = []oci.CapabilityGrant{{
		Path:         "usr/bin/cap-evil",
		Capabilities: []oci.Capability{oci.CAP_SYS_ADMIN, oci.CAP_NET_RAW},
	}}
	allowed, err := oci.Validate(fs, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed.OfType(oci.FindingDangerousCapability)) != 0 {
		t.Fatalf("whitelisted capabilities still reported: %#v", allowed.Findings)
	}
}

func TestHardlinkPreservesWhitedOutPrivilegedInodeAndEscape(t *testing.T) {
	capData := capPayload(t, oci.VFSCapabilityRevision2, true, 0, 1<<oci.CAP_SYS_ADMIN, 0)
	layers := []oci.Layer{{Entries: []oci.Entry{
		{Path: "usr/sbin", Type: oci.TypeDirectory, Mode: 0o755},
		{Path: "usr/sbin/evil", Mode: 0o4755, UID: 0, Xattrs: map[string][]byte{
			oci.SecurityCapabilityXattr: capData,
		}},
	}}, {Entries: []oci.Entry{
		{Path: "usr/local/bin", Type: oci.TypeDirectory, Mode: 0o755},
		{Path: "usr/local/bin/evil-link", Type: oci.TypeHardlink, Linkname: "/usr/sbin/evil"},
		{Path: "usr/sbin/.wh.evil"},
		{Path: "escape/link", Type: oci.TypeHardlink, Linkname: "../../outside/evil"},
	}}}

	fs, err := oci.Merge(layers...)
	if err != nil {
		t.Fatal(err)
	}
	link, err := fs.Stat("usr/local/bin/evil-link")
	if err != nil {
		t.Fatalf("hardlink missing: %v", err)
	}
	if !link.SetUID() {
		t.Fatalf("hardlink did not inherit SUID mode: %#o", link.Mode)
	}
	if link.Capability == nil || !link.Capability.Has(oci.CAP_SYS_ADMIN) {
		t.Fatalf("hardlink did not inherit capability: %#v", link.Capability)
	}
	if link.ResolvedHardlinkTarget != "usr/sbin/evil" {
		t.Fatalf("resolved target = %q", link.ResolvedHardlinkTarget)
	}
	if _, err := fs.Stat("usr/sbin/evil"); err == nil {
		t.Fatal("whiteouted original path should be absent")
	}

	report, err := oci.Validate(fs)
	if err != nil {
		t.Fatal(err)
	}
	var foundPrivilegedLink bool
	for _, finding := range report.Findings {
		if finding.Path == "usr/local/bin/evil-link" &&
			(finding.Type == oci.FindingSUID || finding.Type == oci.FindingDangerousCapability) {
			foundPrivilegedLink = true
		}
	}
	if !foundPrivilegedLink {
		t.Fatalf("privileged hardlink findings missing: %#v", report.Findings)
	}
	escapes := report.OfType(oci.FindingHardlinkEscape)
	if len(escapes) != 1 || escapes[0].Path != "escape/link" {
		t.Fatalf("hardlink escape findings = %#v", escapes)
	}
}

func TestSymlinkCrossLayerRestrictedAndEscape(t *testing.T) {
	layers := []oci.Layer{
		{Entries: []oci.Entry{
			{Path: "etc", Type: oci.TypeDirectory, Mode: 0o755},
			{Path: "root", Type: oci.TypeDirectory, Mode: 0o700},
			{Path: "app", Type: oci.TypeDirectory, Mode: 0o755},
		}},
		{Entries: []oci.Entry{
			{Path: "app/to-etc", Type: oci.TypeSymlink, Target: "../etc"},
			{Path: "abs-root", Type: oci.TypeSymlink, Target: "/root/.ssh"},
			{Path: "tmp/escape", Type: oci.TypeSymlink, Target: "../../outside/etc"},
		}},
	}
	fs, err := oci.Merge(layers...)
	if err != nil {
		t.Fatal(err)
	}

	report, err := oci.Validate(fs)
	if err != nil {
		t.Fatal(err)
	}
	links := report.OfType(oci.FindingSymlinkEscape)
	if len(links) < 3 {
		t.Fatalf("symlink findings = %#v", links)
	}

	var crossLayer bool
	var rootRestricted bool
	var escape bool
	for _, finding := range links {
		switch finding.Path {
		case "app/to-etc":
			if finding.RestrictedDir != "etc" || !finding.CrossLayer {
				t.Fatalf("etc link not cross-layer/restricted: %#v", finding)
			}
			crossLayer = true
		case "abs-root":
			if finding.RestrictedDir != "root" {
				t.Fatalf("absolute /root link not restricted: %#v", finding)
			}
			rootRestricted = true
		case "tmp/escape":
			if finding.Severity != oci.SeverityCritical {
				t.Fatalf("escaping symlink severity = %q, want critical", finding.Severity)
			}
			escape = true
		}
	}
	if !crossLayer || !rootRestricted || !escape {
		t.Fatalf("missing symlink class cross=%v root=%v escape=%v", crossLayer, rootRestricted, escape)
	}
}

func TestDirectoryReplacedByNonDirectoryHidesChildren(t *testing.T) {
	fs, err := oci.Merge(
		oci.Layer{Entries: []oci.Entry{
			{Path: "d", Type: oci.TypeDirectory, Mode: 0o755},
			{Path: "d/child", Mode: 0o644, Content: []byte("child")},
		}},
		oci.Layer{Entries: []oci.Entry{
			{Path: "d", Type: oci.TypeRegular, Mode: 0o644, Content: []byte("file")},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	node, err := fs.Stat("d")
	if err != nil {
		t.Fatal(err)
	}
	if node.IsDir() {
		t.Fatal("d was not replaced by regular file")
	}
	if _, err := fs.Stat("d/child"); err == nil {
		t.Fatal("child remains reachable after directory-to-file replacement")
	}
	var found bool
	for _, residue := range fs.Residue {
		if residue.Path == "d/child" && residue.Reason == oci.ResidueReplace {
			found = true
		}
	}
	if !found {
		t.Fatalf("replaced child residue missing: %#v", fs.Residue)
	}
}

func TestTarRoundTripDecodesModeAndXattr(t *testing.T) {
	capData := capPayload(t, oci.VFSCapabilityRevision2, true, 0, 1<<oci.CAP_SETUID, 0)
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	content := []byte("binary")
	hdr := &tar.Header{
		Name:     "usr/bin/tar-evil",
		Typeflag: tar.TypeReg,
		Mode:     int64(0o4755),
		Uid:      0,
		Gid:      0,
		Size:     int64(len(content)),
		Format:   tar.FormatPAX,
		Xattrs:   map[string]string{oci.SecurityCapabilityXattr: string(capData)},
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layer, err := oci.LoadLayerTar(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	fs, err := oci.Merge(*layer)
	if err != nil {
		t.Fatal(err)
	}
	node, err := fs.Stat("usr/bin/tar-evil")
	if err != nil {
		t.Fatal(err)
	}
	if !node.SetUID() {
		t.Fatalf("tar setuid bit not decoded: %#o", node.Mode)
	}
	if node.Capability == nil || !node.Capability.Has(oci.CAP_SETUID) || !node.Capability.Effective {
		t.Fatalf("tar capability xattr not decoded: %#v", node.Capability)
	}
	report, err := oci.Validate(fs)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasType(oci.FindingSUID) || !report.HasType(oci.FindingDangerousCapability) {
		t.Fatalf("expected SUID and capability findings: %#v", report.Findings)
	}
}
