package netsec

import (
	"net/url"
	"regexp"
	"strings"
)

// commitHashPattern matches exactly 40 hexadecimal characters, the length of
// a Git object identifier. Both upper and lower case are accepted; Git hashes
// compare case-insensitively.
var commitHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// scpRemotePattern matches scp-style Git remotes such as
// "git@github.com:org/repo.git" that lack a URL scheme but are still remote.
var scpRemotePattern = regexp.MustCompile(`^[^/\\:\s]+@[^/\\:\s]+:.+`)

type referenceKind int

const (
	refLocal referenceKind = iota
	refRemote
)

// classifyReference distinguishes local repository paths from remote
// references. Kustomize's own "host?path" form (containing "::") and
// scp-style remotes have no HTTPS representation and are rejected outright.
func classifyReference(value string) (referenceKind, bool) {
	if strings.Contains(value, "::") {
		return refRemote, false
	}
	if scpRemotePattern.MatchString(value) {
		return refRemote, false
	}
	// Kustomize's scheme-less host form, e.g.
	// "github.com/org/repo?ref=v1", puts a dotted hostname before the
	// first slash and a query string after "?". It cannot express an
	// https-pinned reference, so it is rejected.
	if query := strings.IndexByte(value, '?'); query >= 0 {
		prefix := value
		if slash := strings.IndexByte(value, '/'); slash >= 0 && slash < query {
			prefix = value[:slash]
		}
		if strings.Contains(prefix, ".") {
			return refRemote, false
		}
	}
	if strings.Contains(value, "://") {
		return refRemote, true
	}
	return refLocal, true
}

// validateRemote enforces the remote-reference policy:
//
//   - the URL must parse and use the https scheme;
//   - it must not embed userinfo (credentials are a secret-handling risk);
//   - the host must appear in the allowlist (case-insensitive, no port);
//   - a ref query parameter must be present and equal to a 40-hex commit.
//
// Branch names, tag names, short hashes and the absent/default ref all fail.
func validateRemote(value string, allowedHosts []string) (Reason, string) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return ReasonMalformedURL, "malformed remote reference: " + value
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return ReasonSchemeNotHTTPS, "remote references must use https: " + value
	}
	if parsed.User != nil {
		return ReasonURLHasUserInfo, "remote references must not embed userinfo: " + value
	}

	host := parsed.Hostname()
	if host == "" || !hostAllowed(host, allowedHosts) {
		return ReasonHostNotAllowed, "host is not in the allowlist: " + host
	}

	query := parsed.Query()
	refs, present := query["ref"]
	if !present || len(refs) == 0 || refs[0] == "" {
		return ReasonRefMissing, "remote reference must pin a commit with ?ref=<40-hex-sha1>: " + value
	}
	if !commitHashPattern.MatchString(refs[0]) {
		return ReasonRefNotCommit, "ref must be a 40-character hexadecimal commit hash, branches and tags are rejected: " + refs[0]
	}
	return "", ""
}

// hostAllowed performs a case-insensitive membership check. An empty
// allowlist therefore denies every host.
func hostAllowed(host string, allowedHosts []string) bool {
	for _, allowed := range allowedHosts {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return true
		}
	}
	return false
}
