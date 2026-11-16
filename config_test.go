package netsec

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSafeDirectories(t *testing.T) {
	input := `[core]
	repositoryformatversion = 0
[safe]
	directory = /srv/repo
	directory = "/srv/with space/*" ; trailing comment
	directory
[safe "nested"]
	directory = /ignored
[SAFE]
	DIRECTORY = ~/code
	directory = /srv/continued/\
repo/*
`

	got, err := parseSafeDirectories(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/srv/repo",
		"/srv/with space/*",
		"~/code",
		"/srv/continued/repo/*",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSafeDirectories = %#v, want %#v", got, want)
	}
}

func TestSafeDirectoryPatternMatching(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		path    string
		matches bool
	}{
		{name: "star", value: "*", path: "/anywhere/repo", matches: true},
		{name: "exact", value: "/srv/repo", path: "/srv/repo", matches: true},
		{name: "exact mismatch", value: "/srv/repo", path: "/srv/repo2", matches: false},
		{name: "prefix", value: "/srv/shared/*", path: "/srv/shared/repo", matches: true},
		{name: "prefix nested", value: "/srv/shared/*", path: "/srv/shared/team/repo", matches: true},
		{name: "prefix base", value: "/srv/shared/*", path: "/srv/shared", matches: false},
		{name: "prefix boundary", value: "/srv/shared/*", path: "/srv/sharedness", matches: false},
		{name: "root prefix", value: "/*", path: "/srv/repo", matches: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := safeDirectoryEntry{value: test.value, home: "/home/test"}
			if got := entry.matches(test.path); got != test.matches {
				t.Fatalf("%q matches %q = %v, want %v", test.value, test.path, got, test.matches)
			}
		})
	}
}

func TestSafeDirectoryTildeExpansion(t *testing.T) {
	entry := safeDirectoryEntry{value: "~/src/repo", home: "/home/test"}
	if !entry.matches("/home/test/src/repo") {
		t.Fatal("tilde safe.directory entry did not expand to home")
	}
}
