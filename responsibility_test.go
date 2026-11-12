package netsec

import "testing"

const (
	appID    = "com.example.photoapp"
	helperID = "com.example.photoapp.helper"
	teamA    = "TEAMAAAAAA"
	teamB    = "TEAMBBBBBB"
	photos   = "kTCCServicePhotos"
)

func authTable() []Authorization {
	return []Authorization{{
		Service:  photos,
		ClientID: appID,
		TeamID:   teamA,
		Allowed:  true,
	}}
}

func TestAppBundleHelperInheritsAppAuthorization(t *testing.T) {
	processes := []Process{
		{PID: 100, SigningID: appID, TeamID: teamA},
		{
			PID:             200,
			ParentPID:       100,
			SigningID:       helperID,
			TeamID:          teamA,
			AppBundleHelper: true,
		},
	}

	decision := Check(processes, authTable(), 200, photos)
	if !decision.Allowed {
		t.Fatalf("bundle helper should inherit app authorization: %s", decision)
	}
	if decision.ResponsiblePID != 100 {
		t.Fatalf("responsible pid = %d, want 100", decision.ResponsiblePID)
	}
	if decision.ResponsibleIdentity.SigningID != appID {
		t.Fatalf("authorization used %q, want app identity %q",
			decision.ResponsibleIdentity.SigningID, appID)
	}
}

func TestDisclaimedChildIsResponsibleForItself(t *testing.T) {
	processes := []Process{
		{PID: 100, SigningID: appID, TeamID: teamA},
		{
			PID:                     300,
			ParentPID:               100,
			SigningID:               "com.example.untrusted",
			TeamID:                  teamB,
			DisclaimsResponsibility: true,
		},
	}

	decision := Check(processes, authTable(), 300, photos)
	if decision.Allowed {
		t.Fatal("disclaimed child must not inherit the app authorization")
	}
	if decision.ResponsiblePID != 300 {
		t.Fatalf("responsible pid = %d, want 300", decision.ResponsiblePID)
	}
	if decision.Reason != ReasonNoMatchingAuthorization {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonNoMatchingAuthorization)
	}
}

func TestDisclaimedChildCanUseItsOwnAuthorization(t *testing.T) {
	processes := []Process{
		{PID: 100, SigningID: appID, TeamID: teamA},
		{
			PID:                     300,
			ParentPID:               100,
			SigningID:               "com.example.reader",
			TeamID:                  teamB,
			DisclaimsResponsibility: true,
		},
	}
	table := append(authTable(), Authorization{
		Service:  photos,
		ClientID: "com.example.reader",
		TeamID:   teamB,
		Allowed:  true,
	})

	decision := Check(processes, table, 300, photos)
	if !decision.Allowed {
		t.Fatalf("self-responsible child should use its own row: %s", decision)
	}
	if decision.ResponsiblePID != 300 {
		t.Fatalf("responsible pid = %d, want 300", decision.ResponsiblePID)
	}
}

func TestSameIdentifierDifferentTeamDoesNotMatch(t *testing.T) {
	processes := []Process{{
		PID:       400,
		SigningID: appID,
		TeamID:    teamB,
	}}

	decision := Check(processes, authTable(), 400, photos)
	if decision.Allowed {
		t.Fatal("same signing identifier with a different team must not match")
	}
	if decision.Reason != ReasonNoMatchingAuthorization {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonNoMatchingAuthorization)
	}
}

func TestAuthorizationRequiresServiceMatch(t *testing.T) {
	processes := []Process{{
		PID:       400,
		SigningID: appID,
		TeamID:    teamA,
	}}

	if decision := Check(processes, authTable(), 400, "kTCCServiceCamera"); decision.Allowed {
		t.Fatal("authorization for another service must not match")
	}
}

func TestUnsignedInjectedLibraryBlocksInheritance(t *testing.T) {
	processes := []Process{
		{
			PID:       100,
			SigningID: appID,
			TeamID:    teamA,
			InjectedLibraries: []Library{{
				Path: "/tmp/evil.dylib",
			}},
		},
		{
			PID:       500,
			ParentPID: 100,
			SigningID: helperID,
			TeamID:    teamA,
		},
	}

	decision := Check(processes, authTable(), 500, photos)
	if decision.Allowed {
		t.Fatal("child must not inherit authorization from a process with an unsigned library")
	}
	if decision.Reason != ReasonUnsignedInjectedLibrary {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonUnsignedInjectedLibrary)
	}
}

func TestSignedInjectedLibraryDoesNotBlockInheritance(t *testing.T) {
	processes := []Process{
		{
			PID:       100,
			SigningID: appID,
			TeamID:    teamA,
			InjectedLibraries: []Library{{
				Path:      "/Library/Plugins/trusted.dylib",
				SigningID: "com.example.trusted",
				TeamID:    teamA,
			}},
		},
		{
			PID:       500,
			ParentPID: 100,
			SigningID: helperID,
			TeamID:    teamA,
		},
	}

	decision := Check(processes, authTable(), 500, photos)
	if !decision.Allowed {
		t.Fatalf("signed injected library should not block inheritance: %s", decision)
	}
}

func TestDebuggedResponsibleProcessBlocksInheritance(t *testing.T) {
	processes := []Process{
		{PID: 100, SigningID: appID, TeamID: teamA, Debugged: true},
		{PID: 600, ParentPID: 100, SigningID: helperID, TeamID: teamA},
	}

	decision := Check(processes, authTable(), 600, photos)
	if decision.Allowed {
		t.Fatal("debugged responsible process must not pass authorization to a child")
	}
	if decision.Reason != ReasonDebugged {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonDebugged)
	}
}

func TestUnsignedChildCannotInheritParentAuthorization(t *testing.T) {
	processes := []Process{
		{PID: 100, SigningID: appID, TeamID: teamA},
		{PID: 700, ParentPID: 100},
	}

	decision := Check(processes, authTable(), 700, photos)
	if decision.Allowed {
		t.Fatal("unsigned child must not inherit parent authorization")
	}
	if decision.Reason != ReasonUnsignedProcess {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonUnsignedProcess)
	}
}

func TestBundleHelperWithoutContainingAppIsDenied(t *testing.T) {
	processes := []Process{{
		PID:             800,
		SigningID:       appID,
		TeamID:          teamA,
		AppBundleHelper: true,
	}}

	decision := Check(processes, authTable(), 800, photos)
	if decision.Allowed {
		t.Fatal("bundle helper without an app parent must not authorize itself")
	}
	if decision.Reason != ReasonBundleHelperWithoutApp {
		t.Fatalf("reason = %q, want %q", decision.Reason, ReasonBundleHelperWithoutApp)
	}
}

func TestExplicitDenyOverridesAllow(t *testing.T) {
	processes := []Process{{
		PID:       900,
		SigningID: appID,
		TeamID:    teamA,
	}}
	table := []Authorization{
		{Service: photos, ClientID: appID, TeamID: teamA, Allowed: true},
		{Service: photos, ClientID: appID, TeamID: teamA, Allowed: false},
	}

	decision := Check(processes, table, 900, photos)
	if decision.Allowed || decision.Reason != ReasonAuthorizationDenied {
		t.Fatalf("explicit deny must win: %s", decision)
	}
}
