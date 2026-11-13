// Package blackhole implements admission control for customer BGP
// announcements that carry a blackhole (remotely triggered black hole,
// RFC 7999) community.
//
// A routing policy component feeds every announcement received on a
// customer session into Admit together with the session policy. The
// policy lists the prefixes the customer is authorized to announce,
// each with its own maximum acceptable prefix length.
//
// Two classes of announcements are handled:
//
//   - Announcements carrying the configured blackhole community must be
//     a sub-prefix of an authorized prefix and must be a host route:
//     exactly /32 for IPv4 or /128 for IPv6. The MaxPrefixLen field of
//     the matching authorization does not relax this requirement. When
//     admitted the announcement is sanitized so that only the blackhole
//     community and the no-export well-known community remain, every
//     other community (including customer propagation-scope communities
//     such as no-advertise or no-export-subconfed) is dropped, and the
//     next hop is rewritten to the configured blackhole next hop.
//   - Announcements without the blackhole community are admitted when an
//     authorized prefix covers them and their prefix length does not
//     exceed that authorization's MaxPrefixLen. They are returned with
//     their communities and next hop untouched.
//
// In both classes the first ASN of the AS path must equal the customer
// ASN of the session, which stops a customer from originating someone
// else's address space through its own session.
package blackhole

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"
)

// Community is a 32-bit BGP community value as defined in RFC 4360.
type Community uint32

const (
	// NoExport is the NO_EXPORT well-known community (0xFFFFFF01).
	// A route carrying it must not be advertised outside a BGP
	// confederation boundary, or outside an autonomous system when no
	// confederation is used.
	NoExport Community = 0xFFFFFF01

	// NoAdvertise is the NO_ADVERTISE well-known community
	// (0xFFFFFF02). It is exposed so callers can build realistic
	// propagation-scope communities in tests; Admit always strips it
	// from admitted blackhole announcements.
	NoAdvertise Community = 0xFFFFFF02

	// NoExportSubconfed is the NO_EXPORT_SUBCONFED well-known community
	// (0xFFFFFF03). Like NoAdvertise it is stripped from admitted
	// blackhole announcements.
	NoExportSubconfed Community = 0xFFFFFF03
)

// AuthorizedPrefix is one entry of a customer session's authorization
// list. Prefix is the aggregate the customer is allowed to announce and
// MaxPrefixLen is the longest prefix length accepted for ordinary
// announcements covered by that aggregate. MaxPrefixLen never applies
// to blackhole announcements, which are always required to be host
// routes (/32 or /128).
type AuthorizedPrefix struct {
	Prefix       netip.Prefix
	MaxPrefixLen int
}

// Announcement is one route received on the customer session. ASPath is
// ordered with the originating (leftmost) ASN first, matching the wire
// order of an AS_PATH attribute.
type Announcement struct {
	Prefix      netip.Prefix
	Communities []Community
	ASPath      []uint32
	NextHop     netip.Addr
}

// Config describes the policy of a single customer session.
type Config struct {
	// CustomerASN is the autonomous system number the session belongs
	// to. The first ASN of every admitted AS path must equal it.
	CustomerASN uint32

	// Authorized is the set of prefixes the customer may announce with
	// per-entry maximum prefix lengths.
	Authorized []AuthorizedPrefix

	// BlackholeCommunity is the community that marks an announcement as
	// a blackhole request. It is operator configured because deployment
	// practice varies between the RFC 7999 value (0xFFFF029A) and
	// private 16:16 values.
	BlackholeCommunity Community

	// BlackholeNextHop replaces the next hop of every admitted
	// blackhole announcement, pointing the route at the operator's
	// discard infrastructure.
	BlackholeNextHop netip.Addr
}

// Reason identifies why an announcement was rejected.
type Reason int

const (
	// ReasonOK marks an accepted announcement.
	ReasonOK Reason = iota

	// ReasonInvalidConfig marks a malformed session policy.
	ReasonInvalidConfig

	// ReasonMalformedAnnouncement marks an announcement whose prefix is
	// not valid (zero value, masked bits set, or similar).
	ReasonMalformedAnnouncement

	// ReasonEmptyASPath marks an announcement without any ASN in its AS
	// path, so the customer origin cannot be verified.
	ReasonEmptyASPath

	// ReasonOriginMismatch marks an announcement whose first ASN is not
	// the customer ASN.
	ReasonOriginMismatch

	// ReasonNotAuthorized marks a prefix that no authorized prefix
	// covers.
	ReasonNotAuthorized

	// ReasonPrefixTooLong marks an ordinary announcement longer than
	// the MaxPrefixLen of every covering authorization.
	ReasonPrefixTooLong

	// ReasonBlackholeNotHost marks a blackhole announcement whose
	// prefix is not exactly /32 (IPv4) or /128 (IPv6).
	ReasonBlackholeNotHost
)

// String returns a stable identifier for the reason.
func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonInvalidConfig:
		return "invalid_config"
	case ReasonMalformedAnnouncement:
		return "malformed_announcement"
	case ReasonEmptyASPath:
		return "empty_as_path"
	case ReasonOriginMismatch:
		return "origin_mismatch"
	case ReasonNotAuthorized:
		return "not_authorized"
	case ReasonPrefixTooLong:
		return "prefix_too_long"
	case ReasonBlackholeNotHost:
		return "blackhole_not_host"
	default:
		return "unknown"
	}
}

// Decision is the result of evaluating one announcement.
type Decision struct {
	// Accepted reports whether the announcement may be installed.
	Accepted bool

	// Reason is ReasonOK for accepted announcements and otherwise the
	// first violation encountered.
	Reason Reason

	// Announcement is the sanitized announcement when Accepted is true
	// and nil otherwise. It never aliases the input announcement's
	// community slice.
	Announcement *Announcement

	// Blackhole reports whether the accepted announcement was a
	// blackhole request and therefore received community sanitization
	// and a next-hop rewrite.
	Blackhole bool
}

// Admit evaluates announcement against the customer session policy cfg.
// It never mutates the input announcement.
func Admit(cfg Config, announcement Announcement) Decision {
	if err := cfg.validate(); err != nil {
		return Decision{Reason: ReasonInvalidConfig}
	}

	if !announcement.Prefix.IsValid() || announcement.Prefix.Bits() < 0 {
		return Decision{Reason: ReasonMalformedAnnouncement}
	}

	if len(announcement.ASPath) == 0 {
		return Decision{Reason: ReasonEmptyASPath}
	}
	if announcement.ASPath[0] != cfg.CustomerASN {
		return Decision{Reason: ReasonOriginMismatch}
	}

	if hasCommunity(announcement.Communities, cfg.BlackholeCommunity) {
		return admitBlackhole(cfg, announcement)
	}
	return admitOrdinary(cfg, announcement)
}

func admitBlackhole(cfg Config, announcement Announcement) Decision {
	if announcement.Prefix.Bits() != announcement.Prefix.Addr().BitLen() {
		return Decision{Reason: ReasonBlackholeNotHost}
	}
	if !coveredByAny(announcement.Prefix, cfg.Authorized) {
		return Decision{Reason: ReasonNotAuthorized}
	}

	// A blackhole route is only ever kept with the blackhole marker and
	// no-export. Customer propagation-scope communities (no-advertise,
	// no-export-subconfed, private scoping values) are dropped; when the
	// customer already requested no-export it is preserved, otherwise it
	// is added so the sanitized route cannot leak beyond the AS.
	communities := []Community{cfg.BlackholeCommunity}
	if cfg.BlackholeCommunity == NoExport {
		communities = []Community{NoExport}
	} else {
		communities = append(communities, NoExport)
	}

	return Decision{
		Accepted:  true,
		Reason:    ReasonOK,
		Blackhole: true,
		Announcement: &Announcement{
			Prefix:      announcement.Prefix,
			Communities: communities,
			ASPath:      append([]uint32(nil), announcement.ASPath...),
			NextHop:     cfg.BlackholeNextHop,
		},
	}
}

func admitOrdinary(cfg Config, announcement Announcement) Decision {
	for _, entry := range cfg.Authorized {
		if !entry.Prefix.Contains(announcement.Prefix.Addr()) {
			continue
		}
		if announcement.Prefix.Bits() <= entry.MaxPrefixLen {
			return Decision{
				Accepted: true,
				Reason:   ReasonOK,
				Announcement: &Announcement{
					Prefix:      announcement.Prefix,
					Communities: append([]Community(nil), announcement.Communities...),
					ASPath:      append([]uint32(nil), announcement.ASPath...),
					NextHop:     announcement.NextHop,
				},
			}
		}
	}

	for _, entry := range cfg.Authorized {
		if entry.Prefix.Contains(announcement.Prefix.Addr()) {
			return Decision{Reason: ReasonPrefixTooLong}
		}
	}
	return Decision{Reason: ReasonNotAuthorized}
}

func (cfg Config) validate() error {
	if cfg.CustomerASN == 0 {
		return errors.New("blackhole: customer ASN must be set")
	}
	if len(cfg.Authorized) == 0 {
		return errors.New("blackhole: at least one authorized prefix is required")
	}
	if !cfg.BlackholeNextHop.IsValid() {
		return errors.New("blackhole: blackhole next hop must be set")
	}
	for _, entry := range cfg.Authorized {
		if !entry.Prefix.IsValid() || entry.Prefix.Bits() < 0 {
			return errors.New("blackhole: authorized prefix is invalid")
		}
		if entry.MaxPrefixLen < entry.Prefix.Bits() || entry.MaxPrefixLen > entry.Prefix.Addr().BitLen() {
			return errors.New("blackhole: authorized prefix max length out of range")
		}
	}
	return nil
}

func coveredByAny(prefix netip.Prefix, authorized []AuthorizedPrefix) bool {
	for _, entry := range authorized {
		if entry.Prefix.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func hasCommunity(communities []Community, want Community) bool {
	for _, community := range communities {
		if community == want {
			return true
		}
	}
	return false
}

// ParseCommunity parses a community in the "asn:value" 16:16 notation,
// for example "64496:666" or "65535:666". It is provided as a
// convenience for loading configuration; Admit itself works with plain
// Community values.
func ParseCommunity(text string) (Community, error) {
	colon := strings.IndexByte(text, ':')
	if colon <= 0 || colon == len(text)-1 {
		return 0, errors.New("blackhole: community must use ASN:VALUE notation")
	}
	high, err := strconv.ParseUint(text[:colon], 10, 16)
	if err != nil {
		return 0, errors.New("blackhole: community ASN out of 16-bit range")
	}
	low, err := strconv.ParseUint(text[colon+1:], 10, 16)
	if err != nil {
		return 0, errors.New("blackhole: community value out of 16-bit range")
	}
	return Community(high<<16 | low), nil
}

// String renders the community in "asn:value" 16:16 notation.
func (c Community) String() string {
	return strconv.FormatUint(uint64(c>>16), 10) + ":" + strconv.FormatUint(uint64(c)&0xffff, 10)
}
