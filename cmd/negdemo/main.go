// Command negdemo demonstrates a full negotiation round, certificate-bound
// decoding, and a downgrade retry that invalidates the old certificate.
package main

import (
	"encoding/binary"
	"fmt"

	"featureneg"
)

func offer(party string, optional []string) *featureneg.Offer {
	return &featureneg.Offer{
		Party:      party,
		Protocol:   featureneg.Range{Min: 1, Max: 4},
		Required:   []string{"framing"},
		Optional:   optional,
		Preference: []string{"compression", "telemetry", "lowpower"},
		Features: map[string]featureneg.Feature{
			"framing":     {Name: "framing", Versions: featureneg.Range{Min: 1, Max: 3}},
			"compression": {Name: "compression", Versions: featureneg.Range{Min: 1, Max: 2}, Requires: []featureneg.Dep{{Name: "framing", MinVersion: 2}}},
			"telemetry":   {Name: "telemetry", Versions: featureneg.Range{Min: 1, Max: 1}, Conflicts: []string{"lowpower"}},
			"lowpower":    {Name: "lowpower", Versions: featureneg.Range{Min: 1, Max: 1}},
		},
	}
}

func main() {
	optional := []string{"compression", "telemetry", "lowpower"}
	sess := &featureneg.Session{
		ID:     [16]byte{0x2a},
		Local:  offer("client", optional),
		Remote: offer("server", optional),
	}

	out := sess.Negotiate(0)
	fmt.Printf("negotiation status: %s (protocol v%d)\n", out.Status, out.ProtocolVersion)
	for _, s := range out.Selected {
		fmt.Printf("  selected %-12s v%d\n", s.Name, s.Version)
	}
	cert := out.Certificate
	sum := cert.Summary()
	fmt.Printf("certificate summary: %x...\n", sum[:8])

	// Decoding is bound to the certificate: only data frames matching
	// this exact negotiation result are accepted.
	dec, err := sess.BindDecoder(cert)
	must(err)
	payload := []byte("first message under the negotiated parameters")
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	frame = append(frame, payload...)
	got, err := dec.Decode(frame)
	must(err)
	fmt.Printf("decoded frame bound to session: %q\n", got)

	// Downgrade: drop telemetry, keep all required capabilities.
	revised := offer("client", []string{"compression", "lowpower"})
	must(sess.RequestDowngrade(revised))
	if _, err := sess.BindDecoder(cert); err != nil {
		fmt.Printf("old certificate rejected after downgrade: %v\n", err)
	}

	out2 := sess.Negotiate(0)
	fmt.Printf("renegotiation status: %s\n", out2.Status)
	for _, s := range out2.Selected {
		fmt.Printf("  selected %-12s v%d\n", s.Name, s.Version)
	}
	sum2 := out2.Certificate.Summary()
	fmt.Printf("new certificate summary: %x...\n", sum2[:8])

	// A downgrade that silently drops a required capability is refused.
	bad := offer("client", nil)
	bad.Required = nil
	if err := sess.RequestDowngrade(bad); err != nil {
		fmt.Printf("required-stripping downgrade refused: %v\n", err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
