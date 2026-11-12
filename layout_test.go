package netsec

import (
	"strings"
	"testing"
)

const (
	shaGood = "aaa111"
	shaEvil = "bbb222"
)

// buildLink records what the "build" step produced.
func buildLink() Link {
	return Link{
		StepName: "build",
		Products: []Artifact{{Path: "dist/app", SHA256: shaGood}},
	}
}

// packageLayout requires the build product and, when strict, disallows
// anything else in the package step's materials.
func packageLayout(strict bool) Layout {
	rules := []Rule{Match("dist/*", "build")}
	if strict {
		rules = append(rules, Disallow("*"))
	}
	return Layout{Steps: []Step{
		{Name: "build"},
		{Name: "package", MaterialRules: rules},
	}}
}

func packageLink(materials ...Artifact) Link {
	return Link{StepName: "package", Materials: materials}
}

// 改名保摘要: the attacker renames the artifact but keeps its digest.
// MATCH must fail because path and sha256 have to be equal together.
func TestVerifyRenamedArtifactSameDigest(t *testing.T) {
	links := map[string]Link{
		"build":   buildLink(),
		"package": packageLink(Artifact{Path: "dist/app-v2", SHA256: shaGood}),
	}
	err := Verify(packageLayout(true), links)
	if err == nil {
		t.Fatal("expected verification to fail for renamed artifact")
	}
	if !strings.Contains(err.Error(), "dist/app") {
		t.Fatalf("error should name the expected path, got: %v", err)
	}
}

// 同路径换摘要: the attacker swaps the file content but keeps the path.
// MATCH must fail because the sha256 no longer matches.
func TestVerifySamePathDifferentDigest(t *testing.T) {
	links := map[string]Link{
		"build":   buildLink(),
		"package": packageLink(Artifact{Path: "dist/app", SHA256: shaEvil}),
	}
	err := Verify(packageLayout(true), links)
	if err == nil {
		t.Fatal("expected verification to fail for tampered digest")
	}
	if !strings.Contains(err.Error(), shaGood) {
		t.Fatalf("error should name the expected digest, got: %v", err)
	}
}

// 遗漏DISALLOW: without a trailing DISALLOW *, a smuggled artifact
// slips through; adding DISALLOW * catches it.
func TestVerifyMissingDisallow(t *testing.T) {
	links := map[string]Link{
		"build": buildLink(),
		"package": packageLink(
			Artifact{Path: "dist/app", SHA256: shaGood},
			Artifact{Path: "evil.sh", SHA256: shaEvil},
		),
	}
	if err := Verify(packageLayout(false), links); err != nil {
		t.Fatalf("without DISALLOW * the smuggled artifact is not caught: %v", err)
	}
	err := Verify(packageLayout(true), links)
	if err == nil {
		t.Fatal("expected DISALLOW * to reject the smuggled artifact")
	}
	if !strings.Contains(err.Error(), "evil.sh") {
		t.Fatalf("error should name the smuggled artifact, got: %v", err)
	}
}

// An artifact already matched must not be matched again: two identical
// build products cannot both be satisfied by a single material.
func TestVerifyArtifactMatchedOnlyOnce(t *testing.T) {
	layout := Layout{Steps: []Step{
		{Name: "build"},
		{Name: "package", MaterialRules: []Rule{
			Match("dist/*", "build"),
		}},
	}}
	links := map[string]Link{
		"build": {
			StepName: "build",
			Products: []Artifact{
				{Path: "dist/app", SHA256: shaGood},
				{Path: "dist/app", SHA256: shaGood},
			},
		},
		"package": packageLink(Artifact{Path: "dist/app", SHA256: shaGood}),
	}
	if err := Verify(layout, links); err == nil {
		t.Fatal("expected MATCH to fail: one material cannot satisfy two products")
	}
}

// Happy path: exact materials, MATCH plus DISALLOW * pass, and ALLOW
// consumes permitted extras before DISALLOW runs.
func TestVerifyHappyPath(t *testing.T) {
	layout := Layout{Steps: []Step{
		{Name: "build"},
		{Name: "package", MaterialRules: []Rule{
			Match("dist/*", "build"),
			Allow("*.log"),
			Disallow("*"),
		}},
	}}
	links := map[string]Link{
		"build": buildLink(),
		"package": packageLink(
			Artifact{Path: "dist/app", SHA256: shaGood},
			Artifact{Path: "build.log", SHA256: "ccc333"},
		),
	}
	if err := Verify(layout, links); err != nil {
		t.Fatalf("expected verification to pass, got: %v", err)
	}
}
