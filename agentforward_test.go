package netsec

import (
	"errors"
	"testing"
)

func TestAuthorizeAgentForwarding(t *testing.T) {
	allow := ServiceConfig{AllowAgentForwarding: true}
	deny := ServiceConfig{AllowAgentForwarding: false}

	noExt := map[string]string{}
	emptyValExt := map[string]string{ExtNoAgentForwarding: ""}
	otherExt := map[string]string{"permit-pty": ""}

	tests := []struct {
		name       string
		extensions map[string]string
		config     ServiceConfig
		wantErr    bool
		wantSenti  bool
		wantCert   bool
		wantServ   bool
		wantAccept bool
	}{
		{
			name:       "no extension and service allows forwarding",
			extensions: noExt,
			config:     allow,
			wantAccept: true,
		},
		{
			name:       "unrelated extensions and service allows forwarding",
			extensions: otherExt,
			config:     allow,
			wantAccept: true,
		},
		{
			name:       "no extension but service disallows forwarding",
			extensions: noExt,
			config:     deny,
			wantErr:    true,
			wantSenti:  true,
			wantServ:   true,
		},
		{
			name:       "extension present with empty value, service allows",
			extensions: emptyValExt,
			config:     allow,
			wantErr:    true,
			wantSenti:  true,
			wantCert:   true,
		},
		{
			name:       "extension present with empty value, service disallows",
			extensions: emptyValExt,
			config:     deny,
			wantErr:    true,
			wantSenti:  true,
			wantCert:   true,
		},
		{
			name:       "extension present with arbitrary value, service allows",
			extensions: map[string]string{ExtNoAgentForwarding: "true", "permit-pty": ""},
			config:     allow,
			wantErr:    true,
			wantSenti:  true,
			wantCert:   true,
		},
		{
			name:       "nil extensions map with service allows",
			extensions: nil,
			config:     allow,
			wantAccept: true,
		},
		{
			name:       "nil extensions map with service disallows",
			extensions: nil,
			config:     deny,
			wantErr:    true,
			wantSenti:  true,
			wantServ:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AuthorizeAgentForwarding(tt.extensions, tt.config)
			if tt.wantErr && err == nil {
				t.Fatalf("AuthorizeAgentForwarding() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("AuthorizeAgentForwarding() = %v, want nil", err)
			}

			accept, replyErr := AgentForwardingReply(tt.extensions, tt.config)
			if accept != tt.wantAccept {
				t.Fatalf("AgentForwardingReply() accept = %v, want %v", accept, tt.wantAccept)
			}
			if (replyErr == nil) != (err == nil) {
				t.Fatalf("AgentForwardingReply() error = %v, want matching decision", replyErr)
			}

			if tt.wantErr {
				if !errors.Is(err, ErrAgentForwardingDenied) {
					t.Fatalf("error %v does not match ErrAgentForwardingDenied", err)
				}
			}

			var certErr *CertificateExtensionError
			gotCert := errors.As(err, &certErr)
			if gotCert != tt.wantCert {
				t.Fatalf("errors.As(CertificateExtensionError) = %v, want %v", gotCert, tt.wantCert)
			}
			if gotCert && certErr.Extension != ExtNoAgentForwarding {
				t.Fatalf("CertificateExtensionError.Extension = %q, want %q", certErr.Extension, ExtNoAgentForwarding)
			}

			if gotServ := errors.Is(err, ErrAgentForwardingDisabled); gotServ != tt.wantServ {
				t.Fatalf("errors.Is(ErrAgentForwardingDisabled) = %v, want %v", gotServ, tt.wantServ)
			}
		})
	}
}

// TestRepeatedRequestWithExtension models the regression directly: a session
// whose certificate carries no-agent-forwarding requests forwarding twice,
// for example once per channel request. Every request must be rejected, not
// just the first one, and service-level allowance must not override it.
func TestRepeatedRequestWithExtension(t *testing.T) {
	cfg := ServiceConfig{AllowAgentForwarding: true}
	extensions := map[string]string{ExtNoAgentForwarding: ""}

	for attempt := 1; attempt <= 2; attempt++ {
		accept, err := AgentForwardingReply(extensions, cfg)
		if accept {
			t.Fatalf("attempt %d: request accepted despite %s extension", attempt, ExtNoAgentForwarding)
		}
		if err == nil {
			t.Fatalf("attempt %d: nil error despite %s extension", attempt, ExtNoAgentForwarding)
		}

		var certErr *CertificateExtensionError
		if !errors.As(err, &certErr) {
			t.Fatalf("attempt %d: error %v is not a certificate extension denial", attempt, err)
		}
	}
}

// TestRepeatedRequestWithoutExtension verifies repeated requests remain
// governed solely by service configuration when the extension is absent.
func TestRepeatedRequestWithoutExtension(t *testing.T) {
	extensions := map[string]string{"permit-pty": ""}

	allow := ServiceConfig{AllowAgentForwarding: true}
	for attempt := 1; attempt <= 2; attempt++ {
		accept, err := AgentForwardingReply(extensions, allow)
		if !accept || err != nil {
			t.Fatalf("attempt %d: accept=%v err=%v, want accepted", attempt, accept, err)
		}
	}

	deny := ServiceConfig{AllowAgentForwarding: false}
	for attempt := 1; attempt <= 2; attempt++ {
		accept, err := AgentForwardingReply(extensions, deny)
		if accept || err == nil {
			t.Fatalf("attempt %d: accept=%v err=%v, want denied by service", attempt, accept, err)
		}
	}
}
