package flowspec

import (
	"errors"
	"testing"
	"time"
)

// TestEndToEndWirePipeline builds a malicious rule with typed constructors and
// action communities, serializes both the NLRI and the extended-community path
// attribute, decodes them back through the strict binary decoder, and proves
// admission + conflict detection still fire.
func TestEndToEndWirePipeline(t *testing.T) {
	evil := &Rule{
		AF:       AFIPv4,
		ID:       "evil",
		Tenant:   "evil",
		OriginAS: 65066,
		Components: []Component{
			PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.128/25")),
			TermsComponent(TypeProtocol, EqualTerm(6)),
		},
		Actions: ActionSet{Communities: []ExtendedCommunity{TrafficRateCommunity(0, 0)}},
	}

	nlri, err := evil.EncodeNLRI()
	if err != nil {
		t.Fatal(err)
	}
	attr := EncodeExtCommunityAttribute(evil.Actions.Communities)

	// --- host decodes the wire NLRI + path attribute ---
	decoded, err := DecodeNLRI(nlri, AFIPv4)
	if err != nil {
		t.Fatal(err)
	}
	ecs, n, err := DecodeExtCommunityAttribute(attr)
	if err != nil || n != len(attr) {
		t.Fatalf("attr decode n=%d len=%d err=%v", n, len(attr), err)
	}
	decoded.Actions = ActionsFromCommunities(ecs)
	decoded.ID = "evil"
	decoded.Tenant = "evil"
	decoded.OriginAS = 65066

	if decoded.Actions.Verdict() != VerdictDrop {
		t.Fatalf("wire action verdict = %s, want DROP", decoded.Actions.Verdict())
	}

	victim := &Rule{ID: "acme", Tenant: "acme", OriginAS: 65010, Components: []Component{
		PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
	}}
	roas := []ROA{{Prefix: MustParsePrefix("198.51.100.0/24"), OriginAS: 65010, MinLength: 24, MaxLength: 25}}

	rep := NewAuditor(roas).Audit([]*Rule{victim, decoded})
	if !rep.HasCode(CodeCrossTenantBlackhole) || !rep.HasCode(CodeShadowedPermit) {
		t.Fatalf("expected hijack+shadow from decoded wire rule, got %s", summarize(rep))
	}

	// A truncated framed NLRI must fail closed.
	if _, _, err := DecodeFramedNLRI(append([]byte{}, nlri[:len(nlri)-1]...), AFIPv4); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated frame err=%v, want ErrTruncated", err)
	}

	// AdmitAll aligns one decision per rule.
	if ds := AdmitAll([]*Rule{victim, decoded}, roas, time.Time{}); len(ds) != 2 || ds[0].Allowed != true || ds[1].Allowed != false {
		t.Fatalf("AdmitAll mismatch: %+v", ds)
	}
}
