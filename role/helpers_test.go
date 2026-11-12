package role

import "testing"

func TestRoleString(t *testing.T) {
	cases := map[Role]string{
		RoleNochange: "OFPCR_ROLE_NOCHANGE",
		RoleEqual:    "OFPCR_ROLE_EQUAL",
		RoleMaster:   "OFPCR_ROLE_MASTER",
		RoleSlave:    "OFPCR_ROLE_SLAVE",
		Role(42):     "OFPCR_ROLE_UNKNOWN",
	}
	for role, want := range cases {
		if got := role.String(); got != want {
			t.Fatalf("Role(%d).String() = %q, want %q", role, got, want)
		}
	}
}

func TestRoleErrorAndSentinels(t *testing.T) {
	re := &RoleError{Type: ErrTypeRoleRequest, Code: ErrCodeStale, Msg: "stale role request generation id"}
	if re.Error() != "role: stale role request generation id" {
		t.Fatalf("Error() = %q", re.Error())
	}
	if ErrUnknownController.Error() == "" || ErrAlreadyConnected.Error() == "" {
		t.Fatal("sentinel errors must carry a message")
	}
}

func TestIsStateModifying(t *testing.T) {
	modifying := []uint8{TypeFlowMod, TypeGroupMod, TypePortMod, TypeTableMod, TypePacketOut, TypeMeterMod}
	for _, mt := range modifying {
		if !IsStateModifying(mt) {
			t.Fatalf("msgType %d must be state-modifying", mt)
		}
	}
	readOnly := []uint8{0, 4, 20, TypeRoleRequest}
	for _, mt := range readOnly {
		if IsStateModifying(mt) {
			t.Fatalf("msgType %d must not be state-modifying", mt)
		}
	}
}

func TestNilEnvelopeRejected(t *testing.T) {
	a := newConnected(t, c1)
	if err := a.Enqueue(c1, nil); err == nil {
		t.Fatal("nil envelope must be rejected")
	}
}
