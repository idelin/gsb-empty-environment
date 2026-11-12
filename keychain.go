package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// AppIdentifierPrefixToken is the entitlements placeholder expanded to the
// signing team's identifier prefix.
const AppIdentifierPrefixToken = "$(AppIdentifierPrefix)"

var (
	ErrEmptyTeamID        = errors.New("netsec: team ID must not be empty")
	ErrNoAccessGroups     = errors.New("netsec: keychain-access-groups list is empty")
	ErrGroupNotAuthorized = errors.New("netsec: kSecAttrAccessGroup is not in the authorized list")
)

// ExpandAccessGroups expands $(AppIdentifierPrefix) in each
// keychain-access-groups entry. The token is expanded only when it appears at
// the very beginning of an entry, and only once; occurrences elsewhere are
// left as literal text. The token expands to teamID followed by a dot, so
// every expanded group has the form TEAMID.xxx.
func ExpandAccessGroups(groups []string, teamID string) ([]string, error) {
	if teamID == "" {
		return nil, ErrEmptyTeamID
	}
	expanded := make([]string, len(groups))
	for i, group := range groups {
		if strings.HasPrefix(group, AppIdentifierPrefixToken) {
			group = teamID + "." + group[len(AppIdentifierPrefixToken):]
		}
		expanded[i] = group
	}
	return expanded, nil
}

// isWildcard reports whether a provisioning-profile entry is a TEAMID.*
// wildcard, and returns the team prefix it covers (including the dot).
func isWildcard(profileEntry string) (prefix string, ok bool) {
	if !strings.HasSuffix(profileEntry, ".*") {
		return "", false
	}
	team := strings.TrimSuffix(profileEntry, ".*")
	if team == "" || strings.ContainsAny(team, ".*") {
		return "", false
	}
	return team + ".", true
}

// coveredByProfile reports whether a single binary entitlement entry is
// authorized by the provisioning profile. An exact entry authorizes only
// itself; a TEAMID.* wildcard authorizes any group under that team's prefix
// but never groups belonging to another team.
func coveredByProfile(binaryEntry string, profile []string) bool {
	for _, profileEntry := range profile {
		if profileEntry == binaryEntry {
			return true
		}
		if prefix, ok := isWildcard(profileEntry); ok && strings.HasPrefix(binaryEntry, prefix) {
			return true
		}
	}
	return false
}

// AuthorizeAccessGroups verifies that every keychain-access-groups entry in
// the binary's entitlements is authorized by the provisioning profile, i.e.
// the binary entitlements are a subset of the profile entitlements where
// profile TEAMID.* wildcards cover any group under that team only.
func AuthorizeAccessGroups(binary, profile []string) error {
	for _, entry := range binary {
		if !coveredByProfile(entry, profile) {
			return fmt.Errorf("netsec: keychain access group %q is not authorized by the provisioning profile", entry)
		}
	}
	return nil
}

// ResolveAccessGroup determines the effective kSecAttrAccessGroup for a
// keychain query or write. A requested group must be present in the
// authorized list. When no group is requested, the first entry of the
// authorized list is used as the default. An empty authorized list is
// rejected outright.
func ResolveAccessGroup(authorized []string, requested string) (string, error) {
	if len(authorized) == 0 {
		return "", ErrNoAccessGroups
	}
	if requested == "" {
		return authorized[0], nil
	}
	for _, group := range authorized {
		if group == requested {
			return requested, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrGroupNotAuthorized, requested)
}
