package blackhole

import (
	"net/netip"
	"reflect"
	"slices"
	"testing"
)

var (
	customerASN = uint32(64496)
	upstreamASN = uint32(64497)

	// The 203.0.113.0/24 authorization carries a restrictive
	// MaxPrefixLen of 24: blackhole /32s must still be accepted,
	// proving MaxPrefixLen does not constrain blackhole requests.
	testConfig = Config{
		CustomerASN: customerASN,
		Authorized: []AuthorizedPrefix{
			{Prefix: netip.MustParsePrefix("203.0.113.0/24"), MaxPrefixLen: 24},
			{Prefix: netip.MustParsePrefix("198.51.100.0/24"), MaxPrefixLen: 28},
			{Prefix: netip.MustParsePrefix("2001:db8:1::/48"), MaxPrefixLen: 64},
		},
		BlackholeCommunity: Community(0xFFFF029A), // RFC 7999 BLACKHOLE
		BlackholeNextHop:   netip.MustParseAddr("192.0.2.1"),
	}

	customerScope = Community(0x12340001) // private propagation-scope community
)

func mustPrefix(t *testing.T, text string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(text)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", text, err)
	}
	return prefix
}

func TestBlackholeAcceptedSanitizesCommunitiesAndNextHop(t *testing.T) {
	announcement := Announcement{
		Prefix: mustPrefix(t, "203.0.113.7/32"),
		Communities: []Community{
			testConfig.BlackholeCommunity,
			customerScope,
			NoAdvertise,
			NoExportSubconfed,
		},
		ASPath:  []uint32{customerASN},
		NextHop: netip.MustParseAddr("203.0.113.254"),
	}

	decision := Admit(testConfig, announcement)
	if !decision.Accepted || decision.Reason != ReasonOK {
		t.Fatalf("expected acceptance, got %+v", decision)
	}
	if !decision.Blackhole || decision.Announcement == nil {
		t.Fatalf("expected blackhole decision with announcement, got %+v", decision)
	}

	got := decision.Announcement
	if got.NextHop != testConfig.BlackholeNextHop {
		t.Errorf("next hop = %v, want %v", got.NextHop, testConfig.BlackholeNextHop)
	}
	wantCommunities := []Community{testConfig.BlackholeCommunity, NoExport}
	if !reflect.DeepEqual(got.Communities, wantCommunities) {
		t.Errorf("communities = %v, want %v", got.Communities, wantCommunities)
	}
	if got.Prefix != announcement.Prefix {
		t.Errorf("prefix = %v, want %v", got.Prefix, announcement.Prefix)
	}
	if !slices.Equal(got.ASPath, announcement.ASPath) {
		t.Errorf("AS path = %v, want %v", got.ASPath, announcement.ASPath)
	}

	// The input announcement must not be mutated by Admit.
	if len(announcement.Communities) != 4 {
		t.Errorf("input communities were mutated: %v", announcement.Communities)
	}
}

func TestBlackholeAddsNoExportWhenAbsent(t *testing.T) {
	announcement := Announcement{
		Prefix:      mustPrefix(t, "198.51.100.42/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN, upstreamASN},
		NextHop:     netip.MustParseAddr("198.51.100.1"),
	}

	decision := Admit(testConfig, announcement)
	if !decision.Accepted {
		t.Fatalf("expected acceptance, got reason %s", decision.Reason)
	}
	wantCommunities := []Community{testConfig.BlackholeCommunity, NoExport}
	if !reflect.DeepEqual(decision.Announcement.Communities, wantCommunities) {
		t.Errorf("communities = %v, want %v", decision.Announcement.Communities, wantCommunities)
	}
	if !slices.Equal(decision.Announcement.ASPath, []uint32{customerASN, upstreamASN}) {
		t.Errorf("AS path altered: %v", decision.Announcement.ASPath)
	}
}

func TestBlackholeWithScopeCommunityKeepsOnlyNoExport(t *testing.T) {
	// The same announcement carries both the blackhole community and a
	// customer-set propagation-scope community. Only the blackhole
	// marker and no-export may survive; the scope community and the
	// well-known scoping communities must be gone.
	announcement := Announcement{
		Prefix: mustPrefix(t, "203.0.113.9/32"),
		Communities: []Community{
			testConfig.BlackholeCommunity,
			customerScope,
			NoAdvertise,
			NoExport,
		},
		ASPath:  []uint32{customerASN},
		NextHop: netip.MustParseAddr("203.0.113.254"),
	}

	decision := Admit(testConfig, announcement)
	if !decision.Accepted {
		t.Fatalf("expected acceptance, got reason %s", decision.Reason)
	}
	wantCommunities := []Community{testConfig.BlackholeCommunity, NoExport}
	if !reflect.DeepEqual(decision.Announcement.Communities, wantCommunities) {
		t.Fatalf("communities = %v, want %v", decision.Announcement.Communities, wantCommunities)
	}
	for _, banned := range []Community{customerScope, NoAdvertise, NoExportSubconfed} {
		if slices.Contains(decision.Announcement.Communities, banned) {
			t.Errorf("scope community %v survived sanitization", banned)
		}
	}
}

func TestBlackholeUnauthorizedHostIsRejected(t *testing.T) {
	// The core attack: a /32 for address space the customer does not own.
	announcement := Announcement{
		Prefix:      mustPrefix(t, "192.0.2.99/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
		NextHop:     netip.MustParseAddr("203.0.113.254"),
	}

	decision := Admit(testConfig, announcement)
	if decision.Accepted {
		t.Fatalf("unauthorized /32 was accepted: %+v", decision)
	}
	if decision.Reason != ReasonNotAuthorized {
		t.Errorf("reason = %s, want %s", decision.Reason, ReasonNotAuthorized)
	}
	if decision.Announcement != nil {
		t.Errorf("rejected decision must not carry an announcement")
	}
}

func TestBlackholeRejectsNonHostPrefixes(t *testing.T) {
	cases := []string{
		"203.0.113.0/24",
		"203.0.113.0/30",
		"2001:db8:1::/64",
		"2001:db8:1::/127",
	}
	for _, text := range cases {
		announcement := Announcement{
			Prefix:      mustPrefix(t, text),
			Communities: []Community{testConfig.BlackholeCommunity},
			ASPath:      []uint32{customerASN},
			NextHop:     netip.MustParseAddr("203.0.113.254"),
		}

		decision := Admit(testConfig, announcement)
		if decision.Accepted || decision.Reason != ReasonBlackholeNotHost {
			t.Errorf("%s: got accepted=%v reason=%s, want rejection %s",
				text, decision.Accepted, decision.Reason, ReasonBlackholeNotHost)
		}
	}
}

func TestBlackholeHostMustBeSubPrefixOfAuthorization(t *testing.T) {
	// A /128 inside the authorized IPv6 aggregate is accepted; one
	// outside it is not, regardless of the /64 MaxPrefixLen.
	inside := Announcement{
		Prefix:      mustPrefix(t, "2001:db8:1:2:3:4:5:6/128"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
		NextHop:     netip.MustParseAddr("2001:db8:1::1"),
	}
	if decision := Admit(testConfig, inside); !decision.Accepted {
		t.Errorf("authorized /128 rejected: %s", decision.Reason)
	} else if decision.Announcement.NextHop != testConfig.BlackholeNextHop {
		t.Errorf("next hop not rewritten: %v", decision.Announcement.NextHop)
	}

	outside := Announcement{
		Prefix:      mustPrefix(t, "2001:db8:2::1/128"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
		NextHop:     netip.MustParseAddr("2001:db8:1::1"),
	}
	if decision := Admit(testConfig, outside); decision.Accepted || decision.Reason != ReasonNotAuthorized {
		t.Errorf("unauthorized /128: got accepted=%v reason=%s", decision.Accepted, decision.Reason)
	}
}

func TestBlackholeIgnoresMaxPrefixLen(t *testing.T) {
	// The 203.0.113.0/24 authorization caps ordinary announcements at
	// /24, yet a blackhole /32 covered by it must pass.
	announcement := Announcement{
		Prefix:      mustPrefix(t, "203.0.113.250/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
		NextHop:     netip.MustParseAddr("203.0.113.1"),
	}
	if decision := Admit(testConfig, announcement); !decision.Accepted {
		t.Errorf("blackhole /32 under a /24-max authorization rejected: %s", decision.Reason)
	}
}

func TestASPathOriginEnforcement(t *testing.T) {
	base := Announcement{
		Prefix:      mustPrefix(t, "203.0.113.7/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		NextHop:     netip.MustParseAddr("203.0.113.1"),
	}

	wrongOrigin := base
	wrongOrigin.ASPath = []uint32{upstreamASN, customerASN}
	if decision := Admit(testConfig, wrongOrigin); decision.Accepted || decision.Reason != ReasonOriginMismatch {
		t.Errorf("wrong origin: got accepted=%v reason=%s", decision.Accepted, decision.Reason)
	}

	emptyPath := base
	emptyPath.ASPath = nil
	if decision := Admit(testConfig, emptyPath); decision.Accepted || decision.Reason != ReasonEmptyASPath {
		t.Errorf("empty AS path: got accepted=%v reason=%s", decision.Accepted, decision.Reason)
	}

	foreignPrefix := base
	foreignPrefix.Prefix = mustPrefix(t, "198.18.0.1/32")
	foreignPrefix.ASPath = []uint32{customerASN}
	if decision := Admit(testConfig, foreignPrefix); decision.Accepted || decision.Reason != ReasonNotAuthorized {
		t.Errorf("foreign prefix: got accepted=%v reason=%s", decision.Accepted, decision.Reason)
	}
}

func TestOrdinaryPrefixesFollowMaxPrefixLen(t *testing.T) {
	cases := []struct {
		name       string
		prefix     string
		wantReason Reason
	}{
		{"aggregate at max length", "203.0.113.0/24", ReasonOK},
		{"longer than max length", "203.0.113.0/25", ReasonPrefixTooLong},
		{"host route without blackhole", "203.0.113.7/32", ReasonPrefixTooLong},
		{"within second authorization", "198.51.100.0/28", ReasonOK},
		{"beyond second authorization", "198.51.100.16/29", ReasonPrefixTooLong},
		{"ipv6 within max length", "2001:db8:1::/64", ReasonOK},
		{"ipv6 beyond max length", "2001:db8:1::/65", ReasonPrefixTooLong},
		{"unauthorized prefix", "192.0.2.0/24", ReasonNotAuthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			announcement := Announcement{
				Prefix:      mustPrefix(t, tc.prefix),
				Communities: []Community{customerScope, NoExport},
				ASPath:      []uint32{customerASN},
				NextHop:     netip.MustParseAddr("203.0.113.254"),
			}

			decision := Admit(testConfig, announcement)
			if decision.Reason != tc.wantReason {
				t.Fatalf("reason = %s, want %s", decision.Reason, tc.wantReason)
			}
			if tc.wantReason != ReasonOK {
				if decision.Accepted || decision.Announcement != nil {
					t.Fatalf("rejected announcement leaked into decision: %+v", decision)
				}
				return
			}
			if !decision.Accepted || decision.Blackhole {
				t.Fatalf("ordinary route misclassified: %+v", decision)
			}
			// Ordinary routes keep their communities and next hop.
			if !reflect.DeepEqual(decision.Announcement.Communities, announcement.Communities) {
				t.Errorf("communities altered: %v", decision.Announcement.Communities)
			}
			if decision.Announcement.NextHop != announcement.NextHop {
				t.Errorf("next hop altered: %v", decision.Announcement.NextHop)
			}
		})
	}
}

func TestMalformedAnnouncementAndInvalidConfig(t *testing.T) {
	announcement := Announcement{
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
	}
	if decision := Admit(testConfig, announcement); decision.Accepted || decision.Reason != ReasonMalformedAnnouncement {
		t.Errorf("zero prefix: got accepted=%v reason=%s", decision.Accepted, decision.Reason)
	}

	badConfigs := []Config{
		{CustomerASN: 0, Authorized: testConfig.Authorized, BlackholeCommunity: testConfig.BlackholeCommunity, BlackholeNextHop: testConfig.BlackholeNextHop},
		{CustomerASN: customerASN, Authorized: nil, BlackholeCommunity: testConfig.BlackholeCommunity, BlackholeNextHop: testConfig.BlackholeNextHop},
		{CustomerASN: customerASN, Authorized: testConfig.Authorized, BlackholeCommunity: testConfig.BlackholeCommunity},
		{
			CustomerASN:        customerASN,
			Authorized:         []AuthorizedPrefix{{Prefix: netip.MustParsePrefix("203.0.113.0/24"), MaxPrefixLen: 16}},
			BlackholeCommunity: testConfig.BlackholeCommunity,
			BlackholeNextHop:   testConfig.BlackholeNextHop,
		},
	}
	valid := Announcement{
		Prefix:      mustPrefix(t, "203.0.113.7/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
	}
	for i, cfg := range badConfigs {
		if decision := Admit(cfg, valid); decision.Accepted || decision.Reason != ReasonInvalidConfig {
			t.Errorf("bad config #%d: got accepted=%v reason=%s", i, decision.Accepted, decision.Reason)
		}
	}
}

func TestParseCommunityAndString(t *testing.T) {
	community, err := ParseCommunity("65535:666")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := Community(0xFFFF029A); community != want {
		t.Errorf("parsed = %#x, want %#x", community, want)
	}
	if community.String() != "65535:666" {
		t.Errorf("string = %q, want 65535:666", community.String())
	}
	for _, bad := range []string{"", "666", "65535:", ":666", "65536:1", "1:65536"} {
		if _, err := ParseCommunity(bad); err == nil {
			t.Errorf("ParseCommunity(%q) succeeded, want error", bad)
		}
	}
}

func TestBlackholeCommunityEqualToNoExport(t *testing.T) {
	cfg := testConfig
	cfg.BlackholeCommunity = NoExport
	announcement := Announcement{
		Prefix: mustPrefix(t, "203.0.113.7/32"),
		Communities: []Community{
			NoExport,
			customerScope,
			NoAdvertise,
		},
		ASPath:  []uint32{customerASN},
		NextHop: netip.MustParseAddr("203.0.113.254"),
	}

	decision := Admit(cfg, announcement)
	if !decision.Accepted {
		t.Fatalf("expected acceptance, got reason %s", decision.Reason)
	}
	if !reflect.DeepEqual(decision.Announcement.Communities, []Community{NoExport}) {
		t.Errorf("communities = %v, want [no-export] only", decision.Announcement.Communities)
	}
	if decision.Announcement.NextHop != cfg.BlackholeNextHop {
		t.Errorf("next hop = %v, want %v", decision.Announcement.NextHop, cfg.BlackholeNextHop)
	}
}

func TestInvalidAuthorizedPrefixRejectedAsConfigError(t *testing.T) {
	cfg := Config{
		CustomerASN: customerASN,
		Authorized: []AuthorizedPrefix{
			{Prefix: netip.Prefix{}, MaxPrefixLen: 24},
		},
		BlackholeCommunity: testConfig.BlackholeCommunity,
		BlackholeNextHop:   testConfig.BlackholeNextHop,
	}
	announcement := Announcement{
		Prefix:      mustPrefix(t, "203.0.113.7/32"),
		Communities: []Community{testConfig.BlackholeCommunity},
		ASPath:      []uint32{customerASN},
	}
	if decision := Admit(cfg, announcement); decision.Accepted || decision.Reason != ReasonInvalidConfig {
		t.Errorf("got accepted=%v reason=%s, want %s", decision.Accepted, decision.Reason, ReasonInvalidConfig)
	}
	if Reason(99).String() != "unknown" {
		t.Errorf("unknown reason rendering = %q", Reason(99).String())
	}
}
