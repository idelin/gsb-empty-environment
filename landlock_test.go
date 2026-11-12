package netsec

import "testing"

func TestParentChildDirectoryStacking(t *testing.T) {
	base := AccessFSReadFile | AccessFSReadDir | AccessFSWriteFile

	layers := []Layer{
		{
			Handled: base,
			Rules: []PathRule{
				{Path: "/srv", Dir: true, Allowed: AccessFSReadFile | AccessFSReadDir},
				{Path: "/srv/conf", Dir: true, Allowed: AccessFSWriteFile},
			},
		},
		{
			Handled: base,
			Rules: []PathRule{
				{Path: "/srv", Dir: true, Allowed: AccessFSReadFile | AccessFSWriteFile},
				{Path: "/srv/conf", Dir: true, Allowed: AccessFSReadDir},
			},
		},
	}

	profile, err := Compose(ABI1, layers)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	// At /srv/conf/key.pem every layer unions parent and child rules, then
	// the layer results intersect. Both layers end up granting the full base.
	child, err := profile.EffectiveAccess("/srv/conf/key.pem")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	if child.Path != "/srv/conf/key.pem" {
		t.Fatalf("Path = %q", child.Path)
	}
	if child.Allowed != base {
		t.Fatalf("child Allowed = %s, want %s", child.Allowed, base)
	}

	// At /srv/data/feed.txt only the parent rules match. Layer 1 grants
	// READ_FILE|READ_DIR, layer 2 grants READ_FILE|WRITE_FILE, so the
	// intersection keeps READ_FILE alone.
	parentOnly, err := profile.EffectiveAccess("/srv/data/feed.txt")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	if parentOnly.Allowed != AccessFSReadFile {
		t.Fatalf("parent-only Allowed = %s, want READ_FILE", parentOnly.Allowed)
	}
	if !parentOnly.IsDenied(AccessFSReadDir) {
		t.Fatalf("READ_DIR should be denied: %+v", parentOnly)
	}
	if !parentOnly.IsDenied(AccessFSWriteFile) {
		t.Fatalf("WRITE_FILE should be denied: %+v", parentOnly)
	}
	if parentOnly.Allows(AccessFSReadFile) != true {
		t.Fatalf("READ_FILE should be allowed: %+v", parentOnly)
	}

	// A governed right with no matching rule in any layer is fully denied.
	noMatch, err := profile.EffectiveAccess("/etc/passwd")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	if noMatch.Allowed != 0 {
		t.Fatalf("unmatched path Allowed = %s, want 0", noMatch.Allowed)
	}
	if noMatch.Denied != base {
		t.Fatalf("unmatched path Denied = %s, want %s", noMatch.Denied, base)
	}

	// A root directory rule matches every descendant; a non-directory rule
	// matches only its exact path.
	rootProfile, err := Compose(ABI1, []Layer{
		{
			Handled: AccessFSReadFile,
			Rules: []PathRule{
				{Path: "/", Dir: true, Allowed: AccessFSReadFile},
			},
		},
	})
	if err != nil {
		t.Fatalf("root Compose() error = %v", err)
	}
	rootEff, err := rootProfile.EffectiveAccess("/deep/nested/file")
	if err != nil {
		t.Fatalf("root EffectiveAccess() error = %v", err)
	}
	if !rootEff.Allows(AccessFSReadFile) {
		t.Fatalf("root directory rule should match nested paths: %+v", rootEff)
	}

	fileProfile, err := Compose(ABI1, []Layer{
		{
			Handled: AccessFSReadFile,
			Rules: []PathRule{
				{Path: "/exact/file", Allowed: AccessFSReadFile},
			},
		},
	})
	if err != nil {
		t.Fatalf("file Compose() error = %v", err)
	}
	sibling, err := fileProfile.EffectiveAccess("/exact/other")
	if err != nil {
		t.Fatalf("file EffectiveAccess() error = %v", err)
	}
	if sibling.Allowed != 0 {
		t.Fatalf("non-directory rule matched a sibling: %+v", sibling)
	}
}

func TestLegalTighteningAcrossLayers(t *testing.T) {
	readWrite := AccessFSReadFile | AccessFSWriteFile

	progressive := [][]Layer{
		{
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app", Dir: true, Allowed: readWrite}},
			},
		},
		{
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app", Dir: true, Allowed: readWrite}},
			},
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app", Dir: true, Allowed: AccessFSReadFile}},
			},
		},
		{
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app", Dir: true, Allowed: readWrite}},
			},
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app", Dir: true, Allowed: AccessFSReadFile}},
			},
			{
				Handled: readWrite,
				Rules:   []PathRule{{Path: "/app/secret", Dir: true, Allowed: 0}},
			},
		},
	}

	wantAllowed := []AccessFS{readWrite, AccessFSReadFile, 0}
	previous := readWrite

	for index, layers := range progressive {
		profile, err := Compose(ABI1, layers)
		if err != nil {
			t.Fatalf("stage %d Compose() error = %v", index, err)
		}
		eff, err := profile.EffectiveAccess("/app/secret/token")
		if err != nil {
			t.Fatalf("stage %d EffectiveAccess() error = %v", index, err)
		}
		if eff.Allowed != wantAllowed[index] {
			t.Fatalf("stage %d Allowed = %s, want %s",
				index, eff.Allowed, wantAllowed[index])
		}
		if previous&eff.Allowed != eff.Allowed {
			t.Fatalf("stage %d widened rights: previous %s, now %s",
				index, previous, eff.Allowed)
		}
		previous = eff.Allowed
	}
}

func TestRulesUnionWithinLayer(t *testing.T) {
	profile, err := Compose(ABI3, []Layer{
		{
			Handled: AccessFSReadFile | AccessFSWriteFile | AccessFSTruncate,
			Rules: []PathRule{
				{Path: "/data", Dir: true, Allowed: AccessFSReadFile},
				{Path: "/data/out", Dir: true, Allowed: AccessFSWriteFile},
				{Path: "/data/out/dump.bin", Allowed: AccessFSTruncate},
			},
		},
	})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	exact, err := profile.EffectiveAccess("/data/out/dump.bin")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	want := AccessFSReadFile | AccessFSWriteFile | AccessFSTruncate
	if exact.Allowed != want {
		t.Fatalf("Allowed = %s, want union %s", exact.Allowed, want)
	}

	sibling, err := profile.EffectiveAccess("/data/out/other.bin")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	wantSibling := AccessFSReadFile | AccessFSWriteFile
	if sibling.Allowed != wantSibling {
		t.Fatalf("sibling Allowed = %s, want %s", sibling.Allowed, wantSibling)
	}
	if !sibling.IsDenied(AccessFSTruncate) {
		t.Fatalf("file rule must not apply to siblings: %+v", sibling)
	}
}

func TestUngovernedRightsRemainUnrestricted(t *testing.T) {
	profile, err := Compose(ABI3, []Layer{
		{
			Handled: AccessFSReadFile,
			Rules:   []PathRule{{Path: "/x", Dir: true, Allowed: AccessFSReadFile}},
		},
	})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	eff, err := profile.EffectiveAccess("/x/y")
	if err != nil {
		t.Fatalf("EffectiveAccess() error = %v", err)
	}
	if eff.Handled != AccessFSReadFile {
		t.Fatalf("Handled = %s", eff.Handled)
	}
	if !eff.Ungoverned.Contains(AccessFSWriteFile | AccessFSTruncate) {
		t.Fatalf("Ungoverned = %s, want WRITE_FILE|TRUNCATE included",
			eff.Ungoverned)
	}
	if eff.Handled&eff.Ungoverned != 0 {
		t.Fatalf("handled and ungoverned masks overlap: %+v", eff)
	}
	if !eff.Allows(AccessFSReadFile) {
		t.Fatalf("READ_FILE should be allowed: %+v", eff)
	}
}

func TestEffectiveAccessRejectsRelativeTarget(t *testing.T) {
	profile, err := Compose(ABI1, []Layer{
		{
			Handled: AccessFSReadFile,
			Rules:   []PathRule{{Path: "/a", Dir: true, Allowed: AccessFSReadFile}},
		},
	})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if _, err := profile.EffectiveAccess("a/b"); err == nil {
		t.Fatalf("relative target should be rejected")
	}
	if profile.LayerCount() != 1 || profile.ABI() != ABI1 {
		t.Fatalf("profile metadata wrong: %+v", profile)
	}
}
