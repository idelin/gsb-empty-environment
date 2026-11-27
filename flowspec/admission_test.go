package flowspec

import (
	"testing"
	"time"
)

func TestAdmissionMatrix(t *testing.T) {
	roas := []ROA{
		{Prefix: MustParsePrefix("198.51.100.0/24"), OriginAS: 65010, MinLength: 24, MaxLength: 25},
		{Prefix: MustParsePrefix("203.0.113.0/24"), OriginAS: 65020, MaxLength: 24},
		{Prefix: MustParsePrefix("198.18.0.0/24"), OriginAS: 65030, MaxLength: 26},
		{Prefix: MustParsePrefix("10.10.10.0/24"), OriginAS: 65040, MinLength: 25, MaxLength: 26},
		{
			Prefix: MustParsePrefix("172.16.0.0/24"), OriginAS: 65050, MaxLength: 24,
			NotBefore: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			NotAfter:  time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		prefix     string
		as         uint32
		wantAllow  bool
		wantCode   AdmissionCode
		wantStatus RPKIStatus
	}{
		{"exact-roa", "198.51.100.0/24", 65010, true, AdmitOK, RPKIStatusValid},
		{"within-maxlength", "198.51.100.128/25", 65010, true, AdmitOK, RPKIStatusValid},
		{"cross-tenant-origin", "198.51.100.128/25", 65066, false, AdmitOriginMismatch, RPKIStatusInvalid},
		{"not-found", "192.0.2.0/24", 65001, false, AdmitNotFound, RPKIStatusNotFound},
		{"over-wide-same-as", "203.0.112.0/23", 65020, false, AdmitPrefixTooWide, RPKIStatusInvalid},
		{"over-long", "198.18.0.64/27", 65030, false, AdmitPrefixTooLong, RPKIStatusInvalid},
		{"explicit-minlen-rejects-24", "10.10.10.0/24", 65040, false, AdmitPrefixTooWide, RPKIStatusInvalid},
		{"explicit-minlen-allows-25", "10.10.10.128/25", 65040, true, AdmitOK, RPKIStatusValid},
		{"expired-roa", "172.16.0.0/24", 65050, false, AdmitROAExpired, RPKIStatusInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := AdmitPrefix(MustParsePrefix(tc.prefix), tc.as, roas, now)
			if d.Allowed != tc.wantAllow || d.Code != tc.wantCode || d.Status != tc.wantStatus {
				t.Fatalf("got allowed=%v code=%s status=%s; want allow=%v code=%s status=%s (%s)",
					d.Allowed, d.Code, d.Status, tc.wantAllow, tc.wantCode, tc.wantStatus, d.Detail)
			}
			if tc.wantCode == AdmitOriginMismatch && len(d.AuthorizedAS) == 0 {
				t.Fatal("origin mismatch should list authorized AS(es)")
			}
		})
	}
}

func TestAdmitRuleRequiresDestPrefix(t *testing.T) {
	r := &Rule{OriginAS: 65001, Components: []Component{
		TermsComponent(TypeProtocol, EqualTerm(6)),
	}}
	d := Admit(r, nil, time.Time{})
	if d.Allowed || d.Code != AdmitNoDestPrefix {
		t.Fatalf("got %+v, want %s", d, AdmitNoDestPrefix)
	}
}

func TestZeroNowIgnoresExpiry(t *testing.T) {
	roas := []ROA{{
		Prefix: MustParsePrefix("172.16.0.0/24"), OriginAS: 65050, MaxLength: 24,
		NotAfter: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	d := AdmitPrefix(MustParsePrefix("172.16.0.0/24"), 65050, roas, time.Time{})
	if !d.Allowed {
		t.Fatalf("zero Now must disable expiry check: %s", d.Detail)
	}
}
