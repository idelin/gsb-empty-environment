package rpz_test

import (
	"fmt"
	"net/netip"
	"strings"

	"prescreen.local/ps-6517292168/rpz"
)

func Example() {
	zoneText := `
$TTL 3600
@            SOA ns hostmaster ( 1 7200 3600 1209600 3600 )
malware.test.       CNAME .
*.wild.test.        CNAME rpz-drop.
24.0.1.168.192.rpz-ip   CNAME rpz-tcp-only.
48.zz.db8.2001.rpz-ip   CNAME rpz-drop.
`
	zone, err := rpz.ParseZone("policy.example.", strings.NewReader(zoneText))
	if err != nil {
		panic(err)
	}
	engine := rpz.NewEngine([]*rpz.Zone{zone})

	v := engine.Decide(rpz.Request{QName: "malware.test."})
	fmt.Println("malware:", v.Hit, v.Rule.Action)

	v = engine.Decide(rpz.Request{QName: "bare.wild.test."})
	fmt.Println("wildcard subdomain:", v.Hit)

	v = engine.Decide(rpz.Request{
		QName:     "host.test.",
		AnswerIPs: []netip.Addr{netip.MustParseAddr("192.168.1.50")},
	})
	fmt.Println("answer ip:", v.Rule.Action)

	v = engine.Decide(rpz.Request{
		QName:     "v6host.test.",
		AnswerIPs: []netip.Addr{netip.MustParseAddr("2001:db8::dead:beef")},
	})
	fmt.Println("v6 zz answer ip:", v.Rule.Action)

	// Output:
	// malware: true NXDOMAIN
	// wildcard subdomain: true
	// answer ip: TCP-ONLY
	// v6 zz answer ip: DROP
}
