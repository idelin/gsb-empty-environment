package flowspec_test

import (
	"fmt"
	"net"

	flowspec "formal.local/w27-29e3a2d557/flowspec"
)

// Example demonstrates the host pipeline: decode NLRI bytes, admit against a
// ROA set, audit conflicts and resolve a packet.
func Example() {
	// destination 198.51.100.0/24 + ip-protocol tcp(6), end+equal+1-byte = 0x90.
	raw := []byte{0x01, 0x18, 0xC6, 0x33, 0x64, 0x03, 0x90, 0x06}
	comps, err := flowspec.DecodeComponents(raw, flowspec.AFIPv4)
	if err != nil {
		panic(err)
	}
	fmt.Printf("components=%d dest=%s tcp=%d\n", len(comps), comps[0].Prefix.String(), comps[1].Terms[0].Value)

	roas := []flowspec.ROA{{
		Prefix: flowspec.MustParsePrefix("198.51.100.0/24"), OriginAS: 65010,
		MinLength: 24, MaxLength: 25,
	}}
	rules := []*flowspec.Rule{
		{ID: "acme-permit", OriginAS: 65010, Components: []flowspec.Component{
			flowspec.PrefixComponent(flowspec.TypeDestPrefix, flowspec.MustParsePrefix("198.51.100.0/24")),
		}},
		{ID: "evil-blackhole", OriginAS: 65066, Actions: flowspec.ActionSet{
			TrafficRate: &flowspec.TrafficRate{}, // zero rate bits => discard
		}, Components: []flowspec.Component{
			flowspec.PrefixComponent(flowspec.TypeDestPrefix, flowspec.MustParsePrefix("198.51.100.128/25")),
		}},
	}

	rep := flowspec.NewAuditor(roas).Audit(rules)
	for _, f := range rep.Findings {
		fmt.Printf("finding: %s/%s\n", f.Code, f.Severity)
	}

	pkt := &flowspec.Packet{}
	pkt.SetDstIP(net.ParseIP("198.51.100.200"))
	winner, _, verdict := flowspec.Resolve(pkt, rules)
	fmt.Printf("resolve 198.51.100.200 -> %s by %s\n", verdict, winner.ID)

	// Output:
	// components=2 dest=198.51.100.0/24 tcp=6
	// finding: cross-tenant-blackhole-hijack/critical
	// finding: shadowed-permit/critical
	// resolve 198.51.100.200 -> DROP by evil-blackhole
}
