package netsec

import (
	"errors"
	"strings"
	"testing"
)

const sampleLock = `PODS:
  - Alamofire (5.6.4)
  - PrivateKit (1.0.0)

DEPENDENCIES:
  - Alamofire
  - PrivateKit

SPEC REPOS:
  trunk:
    - Alamofire (5.6.4)
  git@github.com:acme/private-specs.git:
    - PrivateKit (1.0.0)

SPEC CHECKSUMS:
  Alamofire: aa11bb22cc33dd44ee55ff6600778899aabbccdd
  PrivateKit: 1122334455667788990011223344556677889900

PODFILE CHECKSUM: deadbeefdeadbeefdeadbeefdeadbeefdeadbeef

COCOAPODS: 1.15.2
`

func TestParseLockfileSections(t *testing.T) {
	lock, err := ParseLockfile(strings.NewReader(sampleLock))
	if err != nil {
		t.Fatalf("parse lockfile: %v", err)
	}

	if got, ok := lock.ChecksumForPod("Alamofire"); !ok || got != "aa11bb22cc33dd44ee55ff6600778899aabbccdd" {
		t.Fatalf("Alamofire checksum = %q (ok=%v)", got, ok)
	}

	if repo, ok := lock.RepoForPod("Alamofire"); !ok || repo != "trunk" {
		t.Fatalf("Alamofire repo = %q (ok=%v), want trunk", repo, ok)
	}
	if repo, ok := lock.RepoForPod("PrivateKit"); !ok || repo != "git@github.com:acme/private-specs.git" {
		t.Fatalf("PrivateKit repo = %q (ok=%v)", repo, ok)
	}

	if _, ok := lock.RepoForPod("Ghost"); ok {
		t.Fatal("unexpected repo entry for unknown pod")
	}
	if _, ok := lock.ChecksumForPod("Ghost"); ok {
		t.Fatal("unexpected checksum entry for unknown pod")
	}
}

func TestParseLockfileCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(sampleLock, "\n", "\r\n")
	lock, err := ParseLockfile(strings.NewReader(crlf))
	if err != nil {
		t.Fatalf("parse CRLF lockfile: %v", err)
	}
	if repo, ok := lock.RepoForPod("PrivateKit"); !ok || repo != "git@github.com:acme/private-specs.git" {
		t.Fatalf("CRLF repo parse = %q (ok=%v)", repo, ok)
	}
}

func TestParseLockfileMalformedChecksum(t *testing.T) {
	bad := "SPEC CHECKSUMS:\n  no-colon-here\n"
	_, err := ParseLockfile(strings.NewReader(bad))
	if !errors.Is(err, ErrInvalidPodspec) {
		t.Fatalf("malformed checksum line: want ErrInvalidPodspec, got %v", err)
	}
}

func TestParseLockfileMalformedReposEntry(t *testing.T) {
	bad := "SPEC REPOS:\n    - orphan-pod (1.0)\n"
	_, err := ParseLockfile(strings.NewReader(bad))
	if !errors.Is(err, ErrInvalidPodspec) {
		t.Fatalf("orphan repo entry: want ErrInvalidPodspec, got %v", err)
	}
}

func TestChecksumCaseInsensitive(t *testing.T) {
	pod := podFixture{
		name:    "Lib",
		version: "1.0",
		repo:    "trunk",
		source:  gitSource("https://github.com/acme/Lib.git", fullCommit),
	}
	data := pod.data(t)
	lock := buildLock(t, []podFixture{pod}, map[string][]byte{pod.name: data})

	// CocoaPods prints lowercase hex, but an uppercase record must match too.
	lock.Checksums[pod.name] = strings.ToUpper(lock.Checksums[pod.name])
	if _, err := NewVerifier(lock).Verify("trunk", SpecPath(pod.name, pod.version), data); err != nil {
		t.Fatalf("uppercase checksum rejected: %v", err)
	}
}
