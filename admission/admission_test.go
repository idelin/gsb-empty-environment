package admission

import (
	"testing"
)

func TestAdmit(t *testing.T) {
	ebgp := Session{PeerAS: 6501, EBGP: true}
	ibgp := Session{PeerAS: 6501, EBGP: false}

	tests := []struct {
		name        string
		sess        Session
		prefix      string
		path        ASPath
		roas        []ROA
		wantReject  bool
		wantRPKI    string
		wantReasons []Reason
	}{
		{
			name:       "legitimate origin is VALID and accepted",
			sess:       ebgp,
			prefix:     "192.0.2.0/24",
			path:       Path(Seq(6501, 6502)),
			roas:       []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject: false,
			wantRPKI:   RPKIValid,
		},
		{
			name:       "more-specific beyond ROA maxLen is NOT_FOUND and accepted",
			sess:       ebgp,
			prefix:     "192.0.2.128/25",
			path:       Path(Seq(6501, 6502)),
			roas:       []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject: false,
			wantRPKI:   RPKINotFound,
		},
		{
			name:        "more-specific covered by another origin AS ROA is INVALID",
			sess:        ebgp,
			prefix:      "198.51.100.128/25",
			path:        Path(Seq(6501, 6502)),
			roas:        []ROA{MustROA(6503, "198.51.100.0/24", 25)},
			wantReject:  true,
			wantRPKI:    RPKIInvalid,
			wantReasons: []Reason{ReasonRPKIInvalid},
		},
		{
			name:        "AS_SET aggregate covered by ROA is INVALID (RFC 6811 NONE origin)",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(Seq(6501), Set(6502, 6503)),
			roas:        []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject:  true,
			wantRPKI:    RPKIInvalid,
			wantReasons: []Reason{ReasonRPKIInvalid},
		},
		{
			name:       "AS_SET aggregate without covering ROA is NOT_FOUND and accepted",
			sess:       ebgp,
			prefix:     "198.51.100.0/24",
			path:       Path(Seq(6501), Set(6502, 6503)),
			roas:       nil,
			wantReject: false,
			wantRPKI:   RPKINotFound,
		},
		{
			name:       "prepending exactly three times stays within the limit",
			sess:       ebgp,
			prefix:     "192.0.2.0/24",
			path:       Path(Seq(6501, 6501, 6501, 6502)),
			roas:       []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject: false,
			wantRPKI:   RPKIValid,
		},
		{
			name:        "prepending the peer AS four times is rejected",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(Seq(6501, 6501, 6501, 6501, 6502)),
			roas:        []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject:  true,
			wantRPKI:    RPKIValid,
			wantReasons: []Reason{ReasonPrependExceeded},
		},
		{
			name:        "first AS not equal to peer AS is rejected",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(Seq(6504, 6502)),
			roas:        []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject:  true,
			wantRPKI:    RPKIValid,
			wantReasons: []Reason{ReasonFirstASMismatch},
		},
		{
			name:        "private 16-bit AS in the path is rejected",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(Seq(6501, 65000)),
			roas:        nil,
			wantReject:  true,
			wantRPKI:    RPKINotFound,
			wantReasons: []Reason{ReasonPrivateAS},
		},
		{
			name:        "private AS hidden inside an AS_SET is still rejected",
			sess:        ebgp,
			prefix:      "198.51.100.0/24",
			path:        Path(Seq(6501), Set(6502, 65000)),
			roas:        nil,
			wantReject:  true,
			wantRPKI:    RPKINotFound,
			wantReasons: []Reason{ReasonPrivateAS},
		},
		{
			name:        "private 32-bit AS in the path is rejected",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(Seq(6501, 4200000000)),
			roas:        nil,
			wantReject:  true,
			wantRPKI:    RPKINotFound,
			wantReasons: []Reason{ReasonPrivateAS},
		},
		{
			name:        "empty AS_PATH on eBGP session is rejected on both path rules",
			sess:        ebgp,
			prefix:      "192.0.2.0/24",
			path:        Path(),
			roas:        nil,
			wantReject:  true,
			wantRPKI:    RPKINotFound,
			wantReasons: []Reason{ReasonEmptyPathOnEBGP, ReasonFirstASMismatch},
		},
		{
			name:       "empty AS_PATH on iBGP session is NOT_FOUND and accepted",
			sess:       ibgp,
			prefix:     "192.0.2.0/24",
			path:       Path(),
			roas:       nil,
			wantReject: false,
			wantRPKI:   RPKINotFound,
		},
		{
			name:        "AS 0 ROA invalidates every covered route and can never validate",
			sess:        ebgp,
			prefix:      "198.51.100.0/24",
			path:        Path(Seq(6501, 6502)),
			roas:        []ROA{MustROA(0, "198.51.100.0/24", 0)},
			wantReject:  true,
			wantRPKI:    RPKIInvalid,
			wantReasons: []Reason{ReasonRPKIInvalid},
		},
		{
			name:   "AS 0 ROA does not override a matching positive authorization",
			sess:   ebgp,
			prefix: "192.0.2.0/24",
			path:   Path(Seq(6501, 6502)),
			roas: []ROA{
				MustROA(0, "192.0.2.0/24", 24),
				MustROA(6502, "192.0.2.0/24", 24),
			},
			wantReject: false,
			wantRPKI:   RPKIValid,
		},
		{
			name:       "IPv6 legitimate origin is VALID and accepted",
			sess:       ebgp,
			prefix:     "2001:db8::/32",
			path:       Path(Seq(6501, 6502)),
			roas:       []ROA{MustROA(6502, "2001:db8::/32", 32)},
			wantReject: false,
			wantRPKI:   RPKIValid,
		},
		{
			name:       "confederation segments are ignored when checking the peer AS",
			sess:       ebgp,
			prefix:     "192.0.2.0/24",
			path:       Path(Segment{Type: ASConfedSequence, ASes: []uint32{6501}}, Seq(6501, 6502)),
			roas:       []ROA{MustROA(6502, "192.0.2.0/24", 24)},
			wantReject: false,
			wantRPKI:   RPKIValid,
		},
		{
			name:        "malformed prefix is rejected before path or ROA checks",
			sess:        ebgp,
			prefix:      "not-a-prefix",
			path:        Path(Seq(6501, 6502)),
			roas:        nil,
			wantReject:  true,
			wantRPKI:    "",
			wantReasons: []Reason{ReasonInvalidPrefix},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := NewROATable(tc.roas...).Admit(tc.sess, tc.prefix, tc.path)
			t.Logf("%s -> %s", tc.name, decision)

			if decision.Rejected != tc.wantReject {
				t.Fatalf("Rejected = %v, want %v", decision.Rejected, tc.wantReject)
			}
			if decision.RPKI != tc.wantRPKI {
				t.Fatalf("RPKI = %q, want %q", decision.RPKI, tc.wantRPKI)
			}
			gotReasons := map[Reason]bool{}
			for _, r := range decision.Reasons {
				gotReasons[r] = true
			}
			for _, want := range tc.wantReasons {
				if !gotReasons[want] {
					t.Fatalf("missing reason %s; got reasons %v", want, decision.Reasons)
				}
			}
			if len(tc.wantReasons) == 0 && len(decision.Reasons) != 0 {
				t.Fatalf("expected no rejection reasons, got %v (%v)", decision.Reasons, decision.Details)
			}
		})
	}
}

func TestReservedASNumbers(t *testing.T) {
	reserved := []uint32{0, 23456, 65535, 65552, 131071, 4294967295}
	for _, as := range reserved {
		category, blocked := classifyAS(as)
		if !blocked || category != "reserved" {
			t.Errorf("AS %d: want reserved/blocked, got %q/%v", as, category, blocked)
		}
	}

	private := []uint32{64512, 65000, 65534, 4200000000, 4294967294}
	for _, as := range private {
		category, blocked := classifyAS(as)
		if !blocked || category != "private" {
			t.Errorf("AS %d: want private/blocked, got %q/%v", as, category, blocked)
		}
	}
}

func TestROACoverageBoundaries(t *testing.T) {
	exact := MustROA(6502, "192.0.2.0/24", 0) // maxLen defaults to 24
	if exact.MaxLen != 24 {
		t.Fatalf("default MaxLen = %d, want 24", exact.MaxLen)
	}
	cases := []struct {
		prefix string
		want   bool
	}{
		{"192.0.2.0/24", true},
		{"192.0.2.128/25", false}, // longer than the authorized max length
		{"192.0.2.0/23", false},   // less specific than the ROA prefix
		{"198.51.100.0/24", false},
		{"2001:db8::/32", false}, // different address family
	}
	for _, tc := range cases {
		p := MustROA(0, tc.prefix, 0).Prefix
		if got := exact.covers(p); got != tc.want {
			t.Errorf("covers(%s) = %v, want %v", tc.prefix, got, tc.want)
		}
	}

	loose := MustROA(6502, "192.0.2.0/24", 26)
	if !loose.covers(MustROA(0, "192.0.2.0/26", 0).Prefix) {
		t.Error("ROA with maxLen 26 should cover a /26 announcement")
	}
}
