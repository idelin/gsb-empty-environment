package s4u2proxy

import "testing"

// baseRequest returns a fully valid S4U2Proxy request; individual tests
// mutate one aspect to exercise a specific denial to be rejected.
func baseRequest() Request {
	return Request{
		User: UserAccount{Name: "alice"},
		Service: ServiceAccount{
			Name:                       "svc-web",
			AllowedToDelegateTo:        []string{"HTTP/api.corp.example"},
			TrustedToAuthForDelegation: true,
		},
		Backend: Backend{SPN: "HTTP/api.corp.example"},
		AdditionalTicket: Ticket{
			Forwardable:  true,
			FromS4U2Self: true,
			ClientName:   "alice",
			ClientRealm:  "CORP.EXAMPLE",
		},
		PAC: PAC{ClientName: "alice", ClientRealm: "CORP.EXAMPLE"},
	}
}

func TestEvaluateAllowsValidRequest(t *testing.T) {
	d := Evaluate(baseRequest())
	if !d.Allowed {
		t.Fatalf("expected allow, got deny: %s", d.Reason)
	}
}

func TestEvaluateAllowsViaRBCD(t *testing.T) {
	r := baseRequest()
	r.Service.AllowedToDelegateTo = nil
	r.Backend.AllowedToActOnBehalfOf = []string{"svc-web"}
	d := Evaluate(r)
	if !d.Allowed {
		t.Fatalf("expected allow via RBCD, got deny: %s", d.Reason)
	}
}

func TestEvaluateDeniesNotDelegatableUser(t *testing.T) {
	r := baseRequest()
	r.User.NotDelegatable = true
	if d := Evaluate(r); d.Allowed {
		t.Fatal("expected deny for account marked not delegatable")
	}
}

func TestEvaluateDeniesProtectedUsersMember(t *testing.T) {
	r := baseRequest()
	r.User.MemberOfProtectedUsers = true
	if d := Evaluate(r); d.Allowed {
		t.Fatal("expected deny for Protected Users member")
	}
}

func TestEvaluateDeniesSPNOutsideAllowedList(t *testing.T) {
	r := baseRequest()
	r.Backend.SPN = "CIFS/files.corp.example"
	if d := Evaluate(r); d.Allowed {
		t.Fatal("expected deny for SPN not in AllowedToDelegateTo and not covered by RBCD")
	}
}

func TestEvaluateDeniesNonForwardableTicket(t *testing.T) {
	r := baseRequest()
	r.AdditionalTicket.Forwardable = false
	if d := Evaluate(r); d.Allowed {
		t.Fatal("expected deny for non-forwardable additional ticket")
	}
}

func TestEvaluateDeniesS4U2SelfTicketWithoutProtocolTransition(t *testing.T) {
	r := baseRequest()
	r.Service.TrustedToAuthForDelegation = false
	if d := Evaluate(r); d.Allowed {
		t.Fatal("expected deny: S4U2Self ticket forwardable only with protocol transition")
	}
}

func TestEvaluateAllowsNonS4U2SelfTicketWithoutProtocolTransition(t *testing.T) {
	r := baseRequest()
	r.Service.TrustedToAuthForDelegation = false
	r.AdditionalTicket.FromS4U2Self = false
	if d := Evaluate(r); !d.Allowed {
		t.Fatalf("expected allow for forwarded (non-S4U2Self) ticket, got deny: %s", d.Reason)
	}
}

func TestEvaluateDeniesPACMismatch(t *testing.T) {
	cases := map[string]PAC{
		"different client name":  {ClientName: "mallory", ClientRealm: "CORP.EXAMPLE"},
		"different client realm": {ClientName: "alice", ClientRealm: "EVIL.EXAMPLE"},
		"empty PAC":              {},
	}
	for name, pac := range cases {
		t.Run(name, func(t *testing.T) {
			r := baseRequest()
			r.PAC = pac
			if d := Evaluate(r); d.Allowed {
				t.Fatal("expected deny for PAC/ticket client mismatch")
			}
		})
	}
}

func TestEvaluateSPNMatchingIsCaseInsensitive(t *testing.T) {
	r := baseRequest()
	r.Backend.SPN = "http/API.CORP.EXAMPLE"
	if d := Evaluate(r); !d.Allowed {
		t.Fatalf("expected case-insensitive SPN match, got deny: %s", d.Reason)
	}
}
