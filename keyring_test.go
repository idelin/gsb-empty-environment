package netsec

import (
	"errors"
	"testing"
)

func classPermissions(other, group, owner, possessor Permission) Permission {
	return other |
		(group << PermGroupShift) |
		(owner << PermOwnerShift) |
		(possessor << PermPossessorShift)
}

func TestSharedHostSessionIsRejected(t *testing.T) {
	request := Request{
		Key: Key{
			OwnerUID:    200,
			OwnerGID:    200,
			Permissions: classPermissions(0, 0, 0, PermRead),
		},
		TargetKeyID: 77,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 10},
		Operation:   OperationRead,
		Graph: KeyringGraph{
			10: {ID: 10, Keys: []KeyID{77}},
		},
		Sandbox: SandboxPolicy{
			Enabled:             true,
			HostSessionKeyring:  10,
			NewAnonymousSession: true,
		},
	}

	result := Evaluate(request)
	if result.Allowed {
		t.Fatalf("shared host session was allowed: %+v", result)
	}
	if result.Reason != ReasonSandboxSharedSession {
		t.Fatalf("reason = %q, want %q", result.Reason, ReasonSandboxSharedSession)
	}
}

func TestSearchDepthSevenIsNotPossession(t *testing.T) {
	graph := KeyringGraph{}
	for i := KeyringID(1); i < 7; i++ {
		graph[i] = Keyring{ID: i, Links: []KeyringID{i + 1}}
	}
	graph[7] = Keyring{ID: 7, Keys: []KeyID{99}}

	request := Request{
		Key: Key{
			OwnerUID:    500,
			OwnerGID:    500,
			Permissions: classPermissions(0, 0, PermRead, 0),
		},
		TargetKeyID: 99,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 1},
		Operation:   OperationRead,
		Graph:       graph,
	}

	result := Evaluate(request)
	if result.Possessor {
		t.Fatalf("depth seven link chain produced possession: %+v", result)
	}
	if result.Allowed {
		t.Fatalf("unreachable key with empty other permissions was allowed: %+v", result)
	}
	if result.Depth != 0 {
		t.Fatalf("reported depth = %d, want 0 for an unreachable key", result.Depth)
	}
}

func TestDepthSixIsPossession(t *testing.T) {
	graph := KeyringGraph{}
	for i := KeyringID(1); i < 6; i++ {
		graph[i] = Keyring{ID: i, Links: []KeyringID{i + 1}}
	}
	graph[6] = Keyring{ID: 6, Keys: []KeyID{99}}

	request := Request{
		Key: Key{
			OwnerUID:    500,
			OwnerGID:    500,
			Permissions: classPermissions(0, 0, 0, PermRead),
		},
		TargetKeyID: 99,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 1},
		Operation:   OperationRead,
		Graph:       graph,
	}

	result := Evaluate(request)
	if !result.Possessor || result.Depth != MaxSearchDepth {
		t.Fatalf("depth six result = %+v, want possession at depth %d", result, MaxSearchDepth)
	}
	if !result.Allowed {
		t.Fatalf("possessor read was denied: %+v", result)
	}
}

func TestGroupPermissionOnly(t *testing.T) {
	key := Key{
		OwnerUID:    500,
		OwnerGID:    1000,
		Permissions: classPermissions(0, PermRead, 0, 0),
	}
	credentials := Credentials{UID: 2000, GID: 1000}

	if got := key.EffectivePermissions(credentials, false); got != PermRead {
		t.Fatalf("effective permissions = %#x, want %#x", got, PermRead)
	}
	if !key.Allows(credentials, false, OperationRead) {
		t.Fatal("group read permission was denied")
	}
	if key.Allows(credentials, false, OperationView) {
		t.Fatal("group-only key unexpectedly allowed view")
	}
}

func TestThreadAndProcessRootsEstablishPossession(t *testing.T) {
	tests := []struct {
		name   string
		caller Caller
		root   KeyringID
	}{
		{
			name:   "thread",
			caller: Caller{UID: 1000, GID: 1000, ThreadKeyring: 10},
			root:   10,
		},
		{
			name:   "process",
			caller: Caller{UID: 1000, GID: 1000, ProcessKeyring: 11},
			root:   11,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := Request{
				Key: Key{
					OwnerUID:    500,
					OwnerGID:    500,
					Permissions: classPermissions(0, 0, 0, PermRead),
				},
				TargetKeyID: 77,
				Caller:      test.caller,
				Operation:   OperationRead,
				Graph:       KeyringGraph{test.root: {ID: test.root, Keys: []KeyID{77}}},
			}

			result := Evaluate(request)
			if !result.Possessor || !result.Allowed {
				t.Fatalf("%s root did not establish possession: %+v", test.name, result)
			}
		})
	}
}

func TestAllOperationsUseTheirPermissionBit(t *testing.T) {
	tests := []struct {
		operation  Operation
		permission Permission
	}{
		{OperationView, PermView},
		{OperationRead, PermRead},
		{OperationSearch, PermSearch},
		{OperationLink, PermLink},
		{OperationSetattr, PermSetattr},
	}

	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			request := Request{
				Key: Key{
					OwnerUID:    500,
					OwnerGID:    500,
					Permissions: test.permission,
				},
				Caller:    Caller{UID: 1000, GID: 1000},
				Operation: test.operation,
			}
			if result := Evaluate(request); !result.Allowed {
				t.Fatalf("%s with its permission bit was denied: %+v", test.operation, result)
			}

			request.Key.Permissions = 0
			if result := Evaluate(request); result.Allowed || result.Reason != ReasonPermissionDenied {
				t.Fatalf("%s without its permission bit was not denied: %+v", test.operation, result)
			}
		})
	}
}

func TestPossessorPermissionsUnionWithApplicableClass(t *testing.T) {
	key := Key{
		OwnerUID:    1000,
		OwnerGID:    1000,
		Permissions: classPermissions(0, 0, PermView, PermRead),
	}
	request := Request{
		Key:         key,
		TargetKeyID: 77,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 10},
		Operation:   OperationView,
		Graph:       KeyringGraph{10: {ID: 10, Keys: []KeyID{77}}},
	}

	result := Evaluate(request)
	if !result.Possessor {
		t.Fatalf("directly linked key was not possessed: %+v", result)
	}
	if got := result.EffectivePermissions; got != PermView|PermRead {
		t.Fatalf("effective permissions = %#x, want %#x", got, PermView|PermRead)
	}
	if !result.Allowed {
		t.Fatalf("possessor and owner permission union was denied: %+v", result)
	}
}

func TestCompliantSandboxSession(t *testing.T) {
	request := Request{
		Key: Key{
			OwnerUID:    1000,
			OwnerGID:    1000,
			Permissions: classPermissions(0, 0, PermRead, 0),
		},
		TargetKeyID: 77,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 100},
		Operation:   OperationRead,
		Graph: KeyringGraph{
			1: {
				ID:    1,
				Links: []KeyringID{2},
			},
			2: {ID: 2},
			100: {
				ID:           100,
				Description:  "",
				Anonymous:    true,
				NewlyCreated: true,
				Keys:         []KeyID{77},
			},
		},
		Sandbox: SandboxPolicy{
			Enabled:             true,
			HostSessionKeyring:  1,
			HostKeyrings:        []KeyringID{1},
			NewAnonymousSession: true,
		},
	}

	result := Evaluate(request)
	if !result.Allowed {
		t.Fatalf("compliant sandbox was denied: %+v", result)
	}
	if !result.Possessor || result.Depth != 1 {
		t.Fatalf("compliant sandbox result = %+v, want possession at depth 1", result)
	}
}

func TestSandboxLinkedIntoHostIsRejected(t *testing.T) {
	request := Request{
		Key: Key{
			OwnerUID:    1000,
			OwnerGID:    1000,
			Permissions: classPermissions(0, 0, PermRead, 0),
		},
		TargetKeyID: 77,
		Caller:      Caller{UID: 1000, GID: 1000, SessionKeyring: 100},
		Operation:   OperationRead,
		Graph: KeyringGraph{
			1:   {ID: 1, Links: []KeyringID{100}},
			100: {ID: 100, Anonymous: true, NewlyCreated: true, Keys: []KeyID{77}},
		},
		Sandbox: SandboxPolicy{
			Enabled:             true,
			HostKeyrings:        []KeyringID{1},
			NewAnonymousSession: true,
		},
	}

	result := Evaluate(request)
	if result.Allowed {
		t.Fatalf("sandbox linked into host was allowed: %+v", result)
	}
	if result.Reason != ReasonSandboxLinkedToHost {
		t.Fatalf("reason = %q, want %q", result.Reason, ReasonSandboxLinkedToHost)
	}
}

func TestCheckReturnsTypedDenial(t *testing.T) {
	request := Request{
		Key:         Key{OwnerUID: 1, OwnerGID: 1},
		TargetKeyID: 9,
		Caller:      Caller{UID: 2, GID: 2, SessionKeyring: 10},
		Operation:   OperationRead,
		Graph:       KeyringGraph{10: {ID: 10, Keys: []KeyID{9}}},
	}

	err := Check(request)
	var denied *AccessDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Check error = %v, want *AccessDeniedError", err)
	}
	if denied.Result.Reason != ReasonPermissionDenied {
		t.Fatalf("denial reason = %q, want %q", denied.Result.Reason, ReasonPermissionDenied)
	}
}

func TestUnknownOperationIsDenied(t *testing.T) {
	result := Evaluate(Request{Operation: Operation("write")})
	if result.Allowed {
		t.Fatalf("unknown operation was allowed: %+v", result)
	}
	if result.Reason != ReasonUnknownOperation {
		t.Fatalf("reason = %q, want %q", result.Reason, ReasonUnknownOperation)
	}
}
