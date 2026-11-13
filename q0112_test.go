package netsec

import (
	"errors"
	"reflect"
	"testing"
)

const (
	q0112SIDAlice      SID = "S-1-1-1"
	q0112SIDBob        SID = "S-1-1-2"
	q0112SIDAdmins     SID = "S-1-1-3"
	q0112SIDBackup     SID = "S-1-1-4"
	q0112SIDAuditors   SID = "S-1-1-5"
	q0112SIDDenyOnly   SID = "S-1-1-6"
	q0112SIDDisabled   SID = "S-1-1-7"
	q0112SIDRestricted SID = "S-1-1-8"
	q0112SIDSandbox    SID = "S-1-1-9"
)

func q0112BaseToken() Token {
	return Token{
		UserSID:           q0112SIDAlice,
		EnabledGroups:     []SID{q0112SIDAdmins, q0112SIDBackup},
		DenyOnlyGroups:    []SID{q0112SIDDenyOnly},
		DisabledGroups:    []SID{q0112SIDDisabled, q0112SIDAuditors},
		EnabledPrivileges: []Privilege{SeBackupPrivilege, SeTakeOwnershipPrivilege},
		IntegrityLevel:    IntegrityHigh,
		RestrictedSIDs:    []SID{q0112SIDRestricted},
	}
}

func q0112AccessToken() Token {
	return Token{
		UserSID:        q0112SIDAlice,
		EnabledGroups:  []SID{q0112SIDAdmins},
		DenyOnlyGroups: []SID{q0112SIDDenyOnly},
		DisabledGroups: []SID{q0112SIDDisabled},
		IntegrityLevel: IntegrityMedium,
	}
}

func q0112IntPtr(v IntegrityLevel) *IntegrityLevel { return &v }
func q0112SIDPtr(v SID) *SID                       { return &v }

func TestQ0112DeriveRestrictedOnlyTightens(t *testing.T) {
	base := q0112BaseToken()
	got, err := base.DeriveRestricted(RestrictOptions{
		DenyOnlyGroups:    []SID{q0112SIDAdmins},
		DisabledGroups:    []SID{q0112SIDBackup},
		RemovePrivileges:  []Privilege{SeBackupPrivilege},
		Integrity:         q0112IntPtr(IntegrityMedium),
		AddRestrictedSIDs: []SID{q0112SIDSandbox},
	})
	if err != nil {
		t.Fatalf("DeriveRestricted: %v", err)
	}
	if got.UserSID != q0112SIDAlice {
		t.Fatalf("user changed: %q", got.UserSID)
	}
	if len(got.EnabledGroups) != 0 {
		t.Fatalf("enabled groups = %v, want empty", got.EnabledGroups)
	}
	if want := []SID{q0112SIDDenyOnly, q0112SIDAdmins}; !reflect.DeepEqual(got.DenyOnlyGroups, want) {
		t.Fatalf("deny-only groups = %v, want %v", got.DenyOnlyGroups, want)
	}
	if want := []SID{q0112SIDDisabled, q0112SIDAuditors, q0112SIDBackup}; !reflect.DeepEqual(got.DisabledGroups, want) {
		t.Fatalf("disabled groups = %v, want %v", got.DisabledGroups, want)
	}
	if want := []Privilege{SeTakeOwnershipPrivilege}; !reflect.DeepEqual(got.EnabledPrivileges, want) {
		t.Fatalf("privileges = %v, want %v", got.EnabledPrivileges, want)
	}
	if got.IntegrityLevel != IntegrityMedium {
		t.Fatalf("integrity = %v, want medium", got.IntegrityLevel)
	}
	if want := []SID{q0112SIDRestricted, q0112SIDSandbox}; !reflect.DeepEqual(got.RestrictedSIDs, want) {
		t.Fatalf("restricted SIDs = %v, want %v", got.RestrictedSIDs, want)
	}
	if want := q0112BaseToken(); !reflect.DeepEqual(base, want) {
		t.Fatalf("base token was mutated: %#v", base)
	}
}

func TestQ0112DeriveRestrictedRejectsEscalation(t *testing.T) {
	base := q0112BaseToken()
	cases := []struct {
		name string
		opts RestrictOptions
		want error
	}{
		{"new enabled group", RestrictOptions{EnableGroups: []SID{q0112SIDBob}}, ErrNewIdentity},
		{"new group as deny-only", RestrictOptions{DenyOnlyGroups: []SID{q0112SIDBob}}, ErrNewIdentity},
		{"new privilege", RestrictOptions{AddPrivileges: []Privilege{SeRestorePrivilege}}, ErrPrivilegeEscalation},
		{"raise integrity", RestrictOptions{Integrity: q0112IntPtr(IntegritySystem)}, ErrIntegrityEscalation},
		{"change user", RestrictOptions{UserSID: q0112SIDPtr(q0112SIDBob)}, ErrNewIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := base.DeriveRestricted(tc.opts); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is(err, %v)", err, tc.want)
			}
		})
	}
}

func TestQ0112DeriveRestrictedPositionalForm(t *testing.T) {
	base := q0112BaseToken()
	got, err := base.DeriveRestricted(
		[]SID{q0112SIDAdmins},
		[]SID{q0112SIDBackup},
		[]SID{q0112SIDSandbox},
		[]Privilege{SeBackupPrivilege},
		IntegrityMedium,
	)
	if err != nil {
		t.Fatalf("positional DeriveRestricted: %v", err)
	}
	if !reflect.DeepEqual(got.DenyOnlyGroups, []SID{q0112SIDDenyOnly, q0112SIDBackup}) {
		t.Fatalf("deny-only groups = %v", got.DenyOnlyGroups)
	}
	if !reflect.DeepEqual(got.DisabledGroups, []SID{q0112SIDDisabled, q0112SIDAuditors, q0112SIDAdmins}) {
		t.Fatalf("disabled groups = %v", got.DisabledGroups)
	}
	if !reflect.DeepEqual(got.RestrictedSIDs, []SID{q0112SIDRestricted, q0112SIDSandbox}) {
		t.Fatalf("restricted SIDs = %v", got.RestrictedSIDs)
	}
}

func TestQ0112DenyOnlyGroupNeverAuthorizes(t *testing.T) {
	token := q0112AccessToken()
	sd := SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDDenyOnly, Mask: AccessRead},
		{Type: AceAllow, SID: q0112SIDDisabled, Mask: AccessRead},
	}}

	got := CheckAccess(token, sd, AccessRead)
	if got.Allowed || got.Granted != 0 {
		t.Fatalf("deny-only/disabled group allow ACE granted access: %#v", got)
	}
	if len(got.Evidence) != 2 || got.Evidence[0].Matched || got.Evidence[1].Matched {
		t.Fatalf("disabled/deny-only SID matched an Allow ACE: %#v", got.Evidence)
	}
}

func TestQ0112DenyOnlyGroupStillDenies(t *testing.T) {
	token := q0112AccessToken()
	sd := SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		{Type: AceDeny, SID: q0112SIDDenyOnly, Mask: AccessRead},
	}}

	got := CheckAccess(token, sd, AccessRead)
	if got.Allowed {
		t.Fatal("deny-only group deny ACE did not reject")
	}
	if got.Reason != ReasonExplicitDeny || got.Primary.DeniedBy != 1 {
		t.Fatalf("reason = %v, deniedBy = %d; want explicit deny at ACE 1", got.Reason, got.Primary.DeniedBy)
	}
}

func TestQ0112RestrictedSIDSecondCheck(t *testing.T) {
	token := q0112AccessToken()
	token.RestrictedSIDs = []SID{q0112SIDRestricted}

	t.Run("primary allow is insufficient", func(t *testing.T) {
		sd := SecurityDescriptor{ACL: []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		}}
		got := CheckAccess(token, sd, AccessRead)
		if got.Allowed {
			t.Fatal("restricted check should have failed")
		}
		if got.Restricted == nil || got.Restricted.Allowed {
			t.Fatalf("restricted result = %#v", got.Restricted)
		}
		if got.Reason != ReasonRestrictedDeny {
			t.Fatalf("reason = %v, want %v", got.Reason, ReasonRestrictedDeny)
		}
	})

	t.Run("both checks allow", func(t *testing.T) {
		sd := SecurityDescriptor{ACL: []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
			{Type: AceAllow, SID: q0112SIDRestricted, Mask: AccessRead},
		}}
		got := CheckAccess(token, sd, AccessRead)
		if !got.Allowed || got.Granted != AccessRead {
			t.Fatalf("both checks allowed but result denied: %s", got.Message)
		}
	})

	t.Run("restricted deny wins", func(t *testing.T) {
		sd := SecurityDescriptor{ACL: []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
			{Type: AceDeny, SID: q0112SIDRestricted, Mask: AccessRead},
		}}
		got := CheckAccess(token, sd, AccessRead)
		if got.Allowed {
			t.Fatal("restricted deny was ignored")
		}
		if got.Restricted == nil || got.Restricted.DeniedBy != 1 {
			t.Fatalf("restricted evidence = %#v", got.Restricted)
		}
	})

	t.Run("deny-only remains deny-only in restricted check", func(t *testing.T) {
		sd := SecurityDescriptor{ACL: []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
			{Type: AceAllow, SID: q0112SIDDenyOnly, Mask: AccessRead},
			{Type: AceAllow, SID: q0112SIDRestricted, Mask: AccessRead},
		}}
		got := CheckAccess(token, sd, AccessRead)
		if !got.Allowed {
			t.Fatalf("deny-only group incorrectly granted but request denied: %s", got.Message)
		}
		for _, evidence := range got.Restricted.Evidence {
			if evidence.SID == q0112SIDDenyOnly && evidence.IsAllow && evidence.Matched {
				t.Fatalf("deny-only SID matched an Allow ACE in restricted check: %#v", evidence)
			}
		}
	})
}

func TestQ0112ExplicitDenyOrder(t *testing.T) {
	token := q0112AccessToken()
	tests := []struct {
		name string
		acl  []ACE
		want bool
	}{
		{"deny before allow", []ACE{
			{Type: AceDeny, SID: q0112SIDAlice, Mask: AccessRead},
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		}, false},
		{"deny after allow", []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
			{Type: AceDeny, SID: q0112SIDAlice, Mask: AccessRead},
		}, false},
		{"nonmatching deny ignored", []ACE{
			{Type: AceDeny, SID: q0112SIDBob, Mask: AccessRead},
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		}, true},
		{"deny only applies to its bits", []ACE{
			{Type: AceDeny, SID: q0112SIDAlice, Mask: AccessWrite},
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckAccess(token, SecurityDescriptor{ACL: tc.acl}, AccessRead)
			if got.Allowed != tc.want {
				t.Fatalf("allowed = %v, want %v (%s)", got.Allowed, tc.want, got.Message)
			}
		})
	}
}

func TestQ0112IntegrityBlocksWriteAndChangeOwner(t *testing.T) {
	token := q0112AccessToken()
	token.EnabledPrivileges = []Privilege{SeTakeOwnershipPrivilege}
	sd := SecurityDescriptor{
		IntegrityLevel: IntegrityHigh,
		ACL: []ACE{
			{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessAll},
		},
	}

	if got := CheckAccess(token, sd, AccessRead); !got.Allowed {
		t.Fatalf("read should remain allowed: %s", got.Message)
	}
	if got := CheckAccess(token, sd, AccessWrite); got.Allowed || got.IntegrityBlocked&AccessWrite == 0 {
		t.Fatalf("write crossed a higher integrity label: %#v", got)
	}
	if got := CheckAccess(token, sd, AccessChangeOwner); got.Allowed {
		t.Fatal("ChangeOwner crossed a higher integrity label")
	}
	if got := CheckAccess(token, sd, AccessTakeOwnership); !got.Allowed {
		t.Fatalf("TakeOwnership is not blocked by the stated integrity rule: %s", got.Message)
	}
	if got := CheckAccess(token, sd, AccessRead|AccessWrite); got.Allowed || got.Granted&AccessRead == 0 {
		t.Fatalf("write request should fail while retaining read evidence: %#v", got)
	}

	lower := sd
	lower.IntegrityLevel = IntegrityLow
	if got := CheckAccess(token, lower, AccessWrite); !got.Allowed {
		t.Fatalf("write below the integrity label should be allowed: %s", got.Message)
	}
}

func TestQ0112PrivilegesOnlyCoverExplicitBits(t *testing.T) {
	token := Token{
		UserSID:           q0112SIDAlice,
		EnabledPrivileges: []Privilege{SeBackupPrivilege, SeTakeOwnershipPrivilege},
		IntegrityLevel:    IntegrityHigh,
	}
	empty := SecurityDescriptor{}

	if got := CheckAccess(token, empty, AccessBackupRead); !got.Allowed {
		t.Fatalf("BackupRead privilege did not grant BackupRead: %s", got.Message)
	}
	if got := CheckAccess(token, empty, AccessTakeOwnership); !got.Allowed {
		t.Fatalf("TakeOwnership privilege did not grant TakeOwnership: %s", got.Message)
	}
	if got := CheckAccess(token, empty, AccessRead); got.Allowed {
		t.Fatal("backup privilege granted Read")
	}
	if got := CheckAccess(token, empty, AccessChangeOwner); got.Allowed {
		t.Fatal("TakeOwnership privilege granted ChangeOwner")
	}

	deny := SecurityDescriptor{ACL: []ACE{
		{Type: AceDeny, SID: q0112SIDAlice, Mask: AccessBackupRead},
	}}
	if got := CheckAccess(token, deny, AccessBackupRead); got.Allowed {
		t.Fatal("privilege bypassed an explicit Deny ACE")
	}

	derived, err := token.DeriveRestricted(RestrictOptions{RemovePrivileges: []Privilege{SeBackupPrivilege}})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := CheckAccess(derived, empty, AccessBackupRead); got.Allowed {
		t.Fatal("removed BackupRead privilege still granted access")
	}
	if got := CheckAccess(derived, empty, AccessTakeOwnership); !got.Allowed {
		t.Fatalf("remaining TakeOwnership privilege was lost: %s", got.Message)
	}
}

func TestQ0112PerACEEvidence(t *testing.T) {
	token := q0112AccessToken()
	sd := SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDAdmins, Access: AccessRead},
		{Type: AceDeny, SID: q0112SIDBob, Permissions: AccessRead},
		{Allow: true, SID: q0112SIDAlice, Mask: AccessWrite},
		{Type: AceAllow, SID: q0112SIDAdmins, Mask: AccessExecute},
	}}

	got := CheckAccess(token, sd, AccessRead|AccessWrite)
	if !got.Allowed || len(got.Evidence) != 4 {
		t.Fatalf("allowed = %v, evidence = %d", got.Allowed, len(got.Evidence))
	}
	if !got.Evidence[0].Applied || got.Evidence[0].Cumulative != AccessRead || got.Evidence[0].MatchKind != MatchEnabledGroup {
		t.Fatalf("first evidence = %#v", got.Evidence[0])
	}
	if got.Evidence[1].Matched {
		t.Fatalf("nonmatching deny marked matched: %#v", got.Evidence[1])
	}
	if !got.Evidence[2].Applied || got.Evidence[2].Cumulative != AccessRead|AccessWrite || got.Evidence[2].MatchKind != MatchUser {
		t.Fatalf("third evidence = %#v", got.Evidence[2])
	}
	if !got.Evidence[3].Matched || got.Evidence[3].Applied {
		t.Fatalf("fourth evidence = %#v", got.Evidence[3])
	}
}

func TestQ0112EvidenceIncludesRestrictedPhase(t *testing.T) {
	token := q0112AccessToken()
	token.RestrictedSIDs = []SID{q0112SIDRestricted}
	sd := SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
		{Type: AceAllow, SID: q0112SIDRestricted, Mask: AccessRead},
	}}

	got := CheckAccess(token, sd, AccessRead)
	if !got.Allowed {
		t.Fatalf("denied: %s", got.Message)
	}
	if len(got.PrimaryEvidence) != 2 || len(got.RestrictedEvidence) != 2 || len(got.Evidence) != 4 {
		t.Fatalf("evidence lengths = %d/%d/%d", len(got.PrimaryEvidence), len(got.RestrictedEvidence), len(got.Evidence))
	}
	if got.Evidence[2].Phase != PhaseRestricted || got.Evidence[3].MatchKind != MatchRestrictedSID {
		t.Fatalf("restricted evidence = %#v / %#v", got.Evidence[2], got.Evidence[3])
	}
}

func TestQ0112AccessArgumentOrderAliases(t *testing.T) {
	token := q0112AccessToken()
	sd := SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
	}}

	a := CheckAccess(token, sd, AccessRead)
	b := CheckAccess(sd, token, AccessRead)
	c := AccessCheck(token, sd.ACL, AccessRead)
	d := token.CheckAccess(sd, AccessRead)
	for _, got := range []Decision{a, b, c, d} {
		if !got.Allowed {
			t.Fatalf("alias check denied: %s", got.Message)
		}
	}
	if !reflect.DeepEqual(a.Primary.Evidence, b.Primary.Evidence) {
		t.Fatal("argument order changed evidence")
	}
}

func TestQ0112TokenFieldAliases(t *testing.T) {
	token := Token{
		User:       q0112SIDAlice,
		Groups:     []SID{q0112SIDAdmins},
		DenyOnly:   []SID{q0112SIDDenyOnly},
		Disabled:   []SID{q0112SIDDisabled},
		Privileges: []Privilege{SeBackupPrivilege},
		Integrity:  IntegrityHigh,
	}
	if got := CheckAccess(token, SecurityDescriptor{ACL: []ACE{
		{Type: AceAllow, SID: q0112SIDAlice, Mask: AccessRead},
	}}, AccessRead); !got.Allowed {
		t.Fatalf("alias token check denied: %s", got.Message)
	}

	derived, err := token.DeriveRestricted(RestrictOptions{
		DenyOnlyGroups:   []SID{q0112SIDAdmins},
		RemovePrivileges: []Privilege{SeBackupPrivilege},
	})
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if derived.User != q0112SIDAlice || len(derived.Groups) != 0 || len(derived.Privileges) != 0 {
		t.Fatalf("alias fields were not mirrored: %#v", derived)
	}
}
