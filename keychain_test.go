package netsec

import (
	"errors"
	"testing"
)

func TestExpandAccessGroupsExpandsLeadingToken(t *testing.T) {
	got, err := ExpandAccessGroups([]string{
		"$(AppIdentifierPrefix)com.example.shared",
		"$(AppIdentifierPrefix)com.example.other",
	}, "TEAMID1234")
	if err != nil {
		t.Fatalf("ExpandAccessGroups: %v", err)
	}
	want := []string{"TEAMID1234.com.example.shared", "TEAMID1234.com.example.other"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExpandAccessGroupsExpandsOnlyOnceAtStart(t *testing.T) {
	got, err := ExpandAccessGroups([]string{
		"$(AppIdentifierPrefix)$(AppIdentifierPrefix)double",
		"middle.$(AppIdentifierPrefix)token",
	}, "TEAMID1234")
	if err != nil {
		t.Fatalf("ExpandAccessGroups: %v", err)
	}
	if got[0] != "TEAMID1234.$(AppIdentifierPrefix)double" {
		t.Errorf("nested token expanded more than once: %q", got[0])
	}
	if got[1] != "middle.$(AppIdentifierPrefix)token" {
		t.Errorf("mid-string token must stay literal, got %q", got[1])
	}
}

func TestExpandAccessGroupsRejectsEmptyTeamID(t *testing.T) {
	if _, err := ExpandAccessGroups([]string{"$(AppIdentifierPrefix)x"}, ""); !errors.Is(err, ErrEmptyTeamID) {
		t.Fatalf("err = %v, want ErrEmptyTeamID", err)
	}
}

func TestAuthorizeAccessGroupsExactSubset(t *testing.T) {
	profile := []string{"TEAMID1234.com.example.shared", "TEAMID1234.com.example.other"}
	if err := AuthorizeAccessGroups([]string{"TEAMID1234.com.example.shared"}, profile); err != nil {
		t.Fatalf("exact subset rejected: %v", err)
	}
	if err := AuthorizeAccessGroups(nil, profile); err != nil {
		t.Fatalf("empty binary list must be accepted: %v", err)
	}
}

func TestAuthorizeAccessGroupsRejectsCrossTeamPrefix(t *testing.T) {
	profile := []string{"TEAMID1234.*"}
	binary := []string{"ATTACKER99.com.example.shared"}
	if err := AuthorizeAccessGroups(binary, profile); err == nil {
		t.Fatal("cross-team group with same bundle suffix must be rejected")
	}
}

func TestAuthorizeAccessGroupsWildcardBoundary(t *testing.T) {
	profile := []string{"TEAMID1234.*"}

	if err := AuthorizeAccessGroups([]string{"TEAMID1234.anything.at.all"}, profile); err != nil {
		t.Fatalf("wildcard must cover groups under its own team: %v", err)
	}

	rejected := []string{
		"TEAMID12345.com.example", // prefix must end at the dot boundary
		"TEAMID1234",              // bare team ID without suffix
		"TEAMID1234x.com.example", // team ID is a prefix of the attacker's
		"OTHERTEAM1.com.example",  // different team entirely
	}
	for _, entry := range rejected {
		if err := AuthorizeAccessGroups([]string{entry}, profile); err == nil {
			t.Errorf("wildcard must not cover %q", entry)
		}
	}
}

func TestAuthorizeAccessGroupsWildcardOnlyInProfile(t *testing.T) {
	// A wildcard in the binary entitlements is not itself authorized by an
	// exact profile entry.
	err := AuthorizeAccessGroups([]string{"TEAMID1234.*"}, []string{"TEAMID1234.com.example"})
	if err == nil {
		t.Fatal("binary wildcard must not be authorized by an exact profile entry")
	}
	// But a profile wildcard authorizes a binary wildcard for the same team.
	if err := AuthorizeAccessGroups([]string{"TEAMID1234.*"}, []string{"TEAMID1234.*"}); err != nil {
		t.Fatalf("profile wildcard must cover binary wildcard of same team: %v", err)
	}
}

func TestAuthorizeAccessGroupsRejectsOutOfSubset(t *testing.T) {
	profile := []string{"TEAMID1234.com.example.shared"}
	binary := []string{
		"TEAMID1234.com.example.shared",
		"TEAMID1234.com.example.extra",
	}
	if err := AuthorizeAccessGroups(binary, profile); err == nil {
		t.Fatal("binary entry outside the profile subset must be rejected")
	}
}

func TestResolveAccessGroupDefaultsToFirstEntry(t *testing.T) {
	authorized := []string{"TEAMID1234.com.example.shared", "TEAMID1234.com.example.other"}
	got, err := ResolveAccessGroup(authorized, "")
	if err != nil {
		t.Fatalf("ResolveAccessGroup: %v", err)
	}
	if got != "TEAMID1234.com.example.shared" {
		t.Errorf("default group = %q, want first entry", got)
	}
}

func TestResolveAccessGroupRequiresMembership(t *testing.T) {
	authorized := []string{"TEAMID1234.com.example.shared"}
	got, err := ResolveAccessGroup(authorized, "TEAMID1234.com.example.shared")
	if err != nil || got != "TEAMID1234.com.example.shared" {
		t.Fatalf("authorized group rejected: got %q, err %v", got, err)
	}
	if _, err := ResolveAccessGroup(authorized, "ATTACKER99.com.example.shared"); !errors.Is(err, ErrGroupNotAuthorized) {
		t.Fatalf("err = %v, want ErrGroupNotAuthorized", err)
	}
}

func TestResolveAccessGroupRejectsEmptyList(t *testing.T) {
	if _, err := ResolveAccessGroup(nil, ""); !errors.Is(err, ErrNoAccessGroups) {
		t.Fatalf("err = %v, want ErrNoAccessGroups", err)
	}
	if _, err := ResolveAccessGroup(nil, "TEAMID1234.com.example.shared"); !errors.Is(err, ErrNoAccessGroups) {
		t.Fatalf("err = %v, want ErrNoAccessGroups", err)
	}
}
