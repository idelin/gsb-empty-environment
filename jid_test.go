package netsec

import "testing"

func TestParseJID(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		local  string
		domain string
		res    string
		bare   string
		full   string
	}{
		{
			name:   "domain only",
			raw:    "example.org",
			domain: "example.org",
			bare:   "example.org",
			full:   "example.org",
		},
		{
			name:   "bare",
			raw:    "alice@example.org",
			local:  "alice",
			domain: "example.org",
			bare:   "alice@example.org",
			full:   "alice@example.org",
		},
		{
			name:   "full",
			raw:    "alice@example.org/phone",
			local:  "alice",
			domain: "example.org",
			res:    "phone",
			bare:   "alice@example.org",
			full:   "alice@example.org/phone",
		},
		{
			name:   "domain with resource",
			raw:    "example.org/server",
			domain: "example.org",
			res:    "server",
			bare:   "example.org",
			full:   "example.org/server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jid, err := ParseJID(tt.raw)
			if err != nil {
				t.Fatalf("ParseJID(%q) returned error: %v", tt.raw, err)
			}
			if jid.Local != tt.local || jid.Domain != tt.domain || jid.Resource != tt.res {
				t.Fatalf("ParseJID(%q) = %#v", tt.raw, jid)
			}
			if got := jid.Bare(); got != tt.bare {
				t.Errorf("Bare() = %q, want %q", got, tt.bare)
			}
			if got := jid.Full(); got != tt.full {
				t.Errorf("Full() = %q, want %q", got, tt.full)
			}
		})
	}
}

func TestParseJIDRejectsMalformedAddresses(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		"alice@@example.org",
		"@example.org",
		"alice@",
		"alice@example.org/",
		" alice@example.org",
		"alice@example.org ",
		"alice @example.org",
		"alice@exa mple.org",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseJID(raw); err == nil {
				t.Fatalf("ParseJID(%q) accepted a malformed address", raw)
			}
		})
	}
}
