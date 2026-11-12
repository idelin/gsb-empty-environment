package netsec

import "testing"

func TestClientFromValidation(t *testing.T) {
	bound := "alice@example.org/desktop"
	tests := []struct {
		name       string
		from       string
		action     Action
		reason     string
		wantedFrom string
	}{
		{
			name:       "missing from is filled with bound full JID",
			from:       "",
			action:     ActionAccept,
			wantedFrom: bound,
		},
		{
			name:       "bound full JID is accepted",
			from:       bound,
			action:     ActionAccept,
			wantedFrom: bound,
		},
		{
			name:       "bound bare JID is accepted",
			from:       "alice@example.org",
			action:     ActionAccept,
			wantedFrom: "alice@example.org",
		},
		{
			name:       "JID comparison is case insensitive outside resource",
			from:       "ALICE@EXAMPLE.ORG",
			action:     ActionAccept,
			wantedFrom: "ALICE@EXAMPLE.ORG",
		},
		{
			name:       "another user cannot be impersonated",
			from:       "bob@example.org/phone",
			action:     ActionCloseStream,
			reason:     ReasonFromMismatch,
			wantedFrom: "bob@example.org/phone",
		},
		{
			name:       "another resource of the same user is rejected",
			from:       "alice@example.org/phone",
			action:     ActionCloseStream,
			reason:     ReasonFromMismatch,
			wantedFrom: "alice@example.org/phone",
		},
		{
			name:       "another domain is rejected",
			from:       "alice@evil.example/desktop",
			action:     ActionCloseStream,
			reason:     ReasonFromMismatch,
			wantedFrom: "alice@evil.example/desktop",
		},
		{
			name:       "malformed from is rejected",
			from:       "alice@@example.org",
			action:     ActionCloseStream,
			reason:     ReasonMalformedFrom,
			wantedFrom: "alice@@example.org",
		},
	}

	stream := NewClientStream(bound)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := stream.Validate(Stanza{From: tt.from, To: "bob@example.org"})
			if decision.Action != tt.action {
				t.Fatalf("action = %v, want %v (reason %q)", decision.Action, tt.action, decision.Reason)
			}
			if decision.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", decision.Reason, tt.reason)
			}
			if decision.Stanza.From != tt.wantedFrom {
				t.Errorf("from = %q, want %q", decision.Stanza.From, tt.wantedFrom)
			}
			if tt.action == ActionCloseStream && decision.Condition != ConditionInvalidFrom {
				t.Errorf("condition = %q, want %q", decision.Condition, ConditionInvalidFrom)
			}
		})
	}
}

func TestClientStanzasBeforeBindingAreDropped(t *testing.T) {
	stream := NewClientStream("")
	for _, from := range []string{"", "alice@example.org/desktop", "bob@example.org/phone"} {
		decision := stream.Validate(Stanza{From: from, To: "example.org"})
		if decision.Action != ActionDrop {
			t.Errorf("from %q: action = %v, want drop", from, decision.Action)
		}
		if decision.Reason != ReasonNotBound {
			t.Errorf("from %q: reason = %q, want %q", from, decision.Reason, ReasonNotBound)
		}
		if decision.Err() != ErrDropped {
			t.Errorf("from %q: Err() = %v, want ErrDropped", from, decision.Err())
		}
	}
}

func TestClientWithInvalidBoundJIDDrops(t *testing.T) {
	stream := NewClientStream("alice@@example.org")
	decision := stream.Validate(Stanza{From: "alice@example.org/desktop"})
	if decision.Action != ActionDrop || decision.Reason != ReasonInvalidBoundJID {
		t.Fatalf("decision = %#v, want invalid-bound-jid drop", decision)
	}
}

func TestServerFromDomainValidation(t *testing.T) {
	stream := NewServerStream(
		[]string{"example.org"},
		[]string{"validated.example.org"},
		[]string{"example.org"},
	)

	tests := []struct {
		name   string
		from   string
		action Action
		reason string
	}{
		{
			name:   "authenticated domain",
			from:   "alice@example.org/phone",
			action: ActionAccept,
		},
		{
			name:   "validated domain",
			from:   "alice@validated.example.org/phone",
			action: ActionAccept,
		},
		{
			name:   "domain-only from",
			from:   "example.org",
			action: ActionAccept,
		},
		{
			name:   "domain comparison is case insensitive",
			from:   "alice@EXAMPLE.ORG/phone",
			action: ActionAccept,
		},
		{
			name:   "unauthenticated domain",
			from:   "alice@evil.example/phone",
			action: ActionCloseStream,
			reason: ReasonFromDomainNotTrusted,
		},
		{
			name:   "unauthenticated domain that looks similar",
			from:   "alice@example.org.evil.example/phone",
			action: ActionCloseStream,
			reason: ReasonFromDomainNotTrusted,
		},
		{
			name:   "missing from",
			from:   "",
			action: ActionCloseStream,
			reason: ReasonMissingFrom,
		},
		{
			name:   "malformed from",
			from:   "alice@@example.org",
			action: ActionCloseStream,
			reason: ReasonMalformedFrom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := stream.Validate(Stanza{From: tt.from, To: "bob@example.org"})
			if decision.Action != tt.action {
				t.Fatalf("action = %v, want %v (reason %q)", decision.Action, tt.action, decision.Reason)
			}
			if decision.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", decision.Reason, tt.reason)
			}
			if tt.action == ActionCloseStream && decision.Condition != ConditionInvalidFrom {
				t.Errorf("condition = %q, want %q", decision.Condition, ConditionInvalidFrom)
			}
			var streamErr *StreamError
			if err := decision.Err(); tt.action == ActionCloseStream {
				var ok bool
				streamErr, ok = err.(*StreamError)
				if !ok || streamErr.Condition != ConditionInvalidFrom {
					t.Errorf("Err() = %#v, want invalid-from stream error", err)
				}
			}
		})
	}
}

func TestServerDestinationRouting(t *testing.T) {
	stream := NewServerStream(
		[]string{"example.org"},
		nil,
		[]string{"example.org", "chat.example.org"},
	)

	tests := []struct {
		name   string
		to     string
		action Action
		reason string
	}{
		{
			name:   "locally hosted domain",
			to:     "bob@example.org",
			action: ActionAccept,
		},
		{
			name:   "another locally hosted domain",
			to:     "bob@chat.example.org/phone",
			action: ActionAccept,
		},
		{
			name:   "remote domain is dropped",
			to:     "bob@remote.example",
			action: ActionDrop,
			reason: ReasonToDomainNotHosted,
		},
		{
			name:   "missing to is dropped",
			to:     "",
			action: ActionDrop,
			reason: ReasonMissingTo,
		},
		{
			name:   "malformed to is dropped",
			to:     "bob@@remote.example",
			action: ActionDrop,
			reason: ReasonMalformedTo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := stream.Validate(Stanza{From: "alice@example.org", To: tt.to})
			if decision.Action != tt.action {
				t.Fatalf("action = %v, want %v (reason %q)", decision.Action, tt.action, decision.Reason)
			}
			if decision.Reason != tt.reason {
				t.Errorf("reason = %q, want %q", decision.Reason, tt.reason)
			}
		})
	}
}

func TestValidateXML(t *testing.T) {
	stream := NewClientStream("alice@example.org/desktop")
	decision, err := stream.ValidateXML([]byte(
		`<message xmlns="jabber:client" to="bob@example.org"><body>hello</body></message>`,
	))
	if err != nil {
		t.Fatalf("ValidateXML returned error: %v", err)
	}
	if !decision.Accepted() {
		t.Fatalf("action = %v, want accept", decision.Action)
	}
	if decision.Stanza.From != "alice@example.org/desktop" {
		t.Errorf("from = %q, want bound JID", decision.Stanza.From)
	}
	if decision.Stanza.XMLName.Local != "message" {
		t.Errorf("XMLName.Local = %q, want message", decision.Stanza.XMLName.Local)
	}
	if decision.Stanza.InnerXML != "<body>hello</body>" {
		t.Errorf("InnerXML = %q, want preserved payload", decision.Stanza.InnerXML)
	}

	decision, err = stream.ValidateXML([]byte(
		`<message xmlns="jabber:client" from="bob@example.org" to="alice@example.org"><body>spoof</body></message>`,
	))
	if err != nil {
		t.Fatalf("ValidateXML returned error: %v", err)
	}
	if decision.Action != ActionCloseStream || decision.Condition != ConditionInvalidFrom {
		t.Fatalf("decision = %#v, want invalid-from stream error", decision)
	}
}

func TestUnknownDirectionDrops(t *testing.T) {
	stream := Stream{Direction: Direction(99)}
	decision := stream.Validate(Stanza{From: "alice@example.org"})
	if decision.Action != ActionDrop || decision.Reason != ReasonUnknownDirection {
		t.Fatalf("decision = %#v, want unknown-direction drop", decision)
	}
}

func TestValidateXMLRejectsMalformedXML(t *testing.T) {
	stream := NewClientStream("alice@example.org/desktop")
	if _, err := stream.ValidateXML([]byte(`<message`)); err == nil {
		t.Fatal("ValidateXML accepted malformed XML")
	}
}

func TestAcceptedDecisionErrIsNil(t *testing.T) {
	stream := NewClientStream("alice@example.org/desktop")
	decision := stream.Validate(Stanza{})
	if err := decision.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}
