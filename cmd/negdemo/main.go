// Command negdemo demonstrates a full negotiation round trip: joint
// solving, certificate binding, safe decoding, downgrade and rejection
// reporting.
package main

import (
	"fmt"

	"negproto/negotiate"
)

func vr(min, max int) negotiate.VersionRange { return negotiate.VersionRange{Min: min, Max: max} }

func main() {
	whenV2plus := vr(2, 3)
	client := &negotiate.Profile{Name: "client", Features: []negotiate.FeatureDecl{
		{Name: "tls", Versions: vr(1, 3), Required: true},
		{Name: "compression", Versions: vr(1, 2), Priority: 6,
			Depends: []negotiate.Requirement{{Feature: "tls", Range: vr(2, 3), When: &whenV2plus}}},
		{Name: "legacy-api", Versions: vr(1, 1), Priority: 1, Conflicts: []string{"compression"}},
		{Name: "telemetry", Versions: vr(0, 2), Priority: 3},
	}}
	server := &negotiate.Profile{Name: "server", Features: []negotiate.FeatureDecl{
		{Name: "tls", Versions: vr(2, 3), Required: true},
		{Name: "compression", Versions: vr(1, 2), Priority: 2,
			Depends: []negotiate.Requirement{{Feature: "tls", Range: vr(2, 3), When: &whenV2plus}}},
		{Name: "legacy-api", Versions: vr(1, 1), Priority: 0, Conflicts: []string{"compression"}},
		{Name: "telemetry", Versions: vr(1, 2), Priority: 1},
	}}

	fmt.Println("== negotiate ==")
	res := negotiate.Negotiate(client, server, -1)
	fmt.Printf("status:    %s\n", res.Status)
	fmt.Printf("selection: %v\n", res.Selection)
	fmt.Printf("score:     weight=%d versions=%d (search nodes: %d)\n",
		res.Score.Weight, res.Score.Versions, res.Nodes)

	sess, _, err := negotiate.Establish(client, server, -1)
	if err != nil {
		panic(err)
	}
	cert := sess.Cert
	fmt.Printf("summary:   %x\n", cert.Summary[:8])
	if err := cert.Verify(client, server); err != nil {
		panic(err)
	}
	fmt.Println("certificate verifies against both profiles")

	fmt.Println("\n== bound decoding ==")
	wireData := cert.Marshal()
	if _, err := negotiate.UnmarshalCertificate(wireData, sess.Summary()); err != nil {
		panic(err)
	}
	fmt.Println("decode with current session summary: ok")
	if _, err := negotiate.UnmarshalCertificate(wireData, [32]byte{0xde}); err != nil {
		fmt.Printf("decode with foreign summary:         rejected (%v)\n", err)
	}

	fmt.Println("\n== downgrade (bound to session) ==")
	if _, _, err := sess.Downgrade("telemetry", 1, [32]byte{0xba}); err != nil {
		fmt.Printf("stale downgrade request:  rejected (%v)\n", err)
	}
	newCert, _, err := sess.Downgrade("telemetry", 1, sess.Summary())
	if err != nil {
		panic(err)
	}
	fmt.Printf("downgraded: %v\n", newCert.Selection)
	fmt.Printf("new summary: %x (old %x...)\n", newCert.Summary[:8], cert.Summary[:4])
	if _, err := negotiate.UnmarshalCertificate(cert.Marshal(), sess.Summary()); err != nil {
		fmt.Println("old certificate can no longer decode the session: rejected")
	}
	if _, ok := newCert.Selection["tls"]; !ok {
		panic("required feature silently dropped")
	}
	fmt.Println("required features preserved across downgrade")

	fmt.Println("\n== rejection with reasons and minimal conflict core ==")
	picky := &negotiate.Profile{Name: "picky", Features: []negotiate.FeatureDecl{
		{Name: "tls", Versions: vr(5, 6), Required: true},
		{Name: "hardware-token", Versions: vr(1, 1), Required: true},
	}}
	bad := negotiate.Negotiate(picky, server, -1)
	fmt.Printf("status: %s\n", bad.Status)
	for _, r := range bad.Reasons {
		fmt.Printf("  reason: %s\n", r)
	}
	for _, c := range bad.Core {
		fmt.Printf("  core:   %s\n", c)
	}
}
