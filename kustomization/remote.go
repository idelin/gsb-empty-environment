package kustomization

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	schemePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
	scpPattern    = regexp.MustCompile(`^[^/@:\s]+@[^/@:\s]+:`)
	commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

// isRemoteReference reports whether ref is a remote URL rather than a local
// path. SCP-style git references (git@host:path) are classified as remote so
// that they can be rejected for lacking an https URL.
func isRemoteReference(ref string) bool {
	return schemePattern.MatchString(ref) || scpPattern.MatchString(ref)
}

// checkRemote enforces https transport, hostname whitelisting and immutable
// 40-hex-character commit pins.
func checkRemote(ref, field string, allowed map[string]bool) []Violation {
	return checkRemoteRef(ref, field, allowed, true)
}

// checkHelmRepo enforces https transport and hostname whitelisting for Helm
// chart repositories. Chart versions, rather than commit pins, freeze the
// fetched artifact.
func checkHelmRepo(repo, field string, allowed map[string]bool) []Violation {
	return checkRemoteRef(repo, field, allowed, false)
}

func checkRemoteRef(ref, field string, allowed map[string]bool, requireCommit bool) []Violation {
	if scpPattern.MatchString(ref) {
		return []Violation{{Field: field, Value: ref, Reason: "scp-style references are not allowed; use an https URL pinned to a commit"}}
	}

	u, err := url.Parse(ref)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return []Violation{{Field: field, Value: ref, Reason: "malformed remote reference"}}
	}
	if u.Scheme != "https" {
		return []Violation{{Field: field, Value: ref, Reason: "remote references must use https"}}
	}
	if u.User != nil {
		return []Violation{{Field: field, Value: ref, Reason: "remote references must not embed user information"}}
	}
	host := strings.ToLower(u.Hostname())
	if !allowed[host] {
		return []Violation{{Field: field, Value: ref, Reason: "host " + host + " is not in the remote host allowlist"}}
	}

	if requireCommit {
		pin := u.Query().Get("ref")
		if pin == "" {
			return []Violation{{Field: field, Value: ref, Reason: "remote reference must pin a 40-character commit via ref; default, branch and tag references are rejected"}}
		}
		if !commitPattern.MatchString(pin) {
			return []Violation{{Field: field, Value: ref, Reason: "ref must be a 40-character hexadecimal commit; branch and tag references are rejected"}}
		}
	}
	return nil
}
