package netsec

import "fmt"

func ExampleSolve() {
	in := Input{
		Organizations: []Organization{
			{ID: "alpha-ca", Region: "us", Category: "ca"},
			{ID: "alpha-notary", Region: "us", Category: "notary"},
			{ID: "beta-ca", Region: "eu", Category: "ca"},
			{ID: "gamma-notary", Region: "ap", Category: "notary"},
		},
		Certificates: []Certificate{
			{ID: "cert-a1", Holder: "alpha-ca"},
			{ID: "cert-a2", Holder: "alpha-notary"},
			{ID: "cert-b1", Holder: "beta-ca"},
			{ID: "cert-g1", Holder: "gamma-notary"},
		},
		// alpha-ca controls alpha-notary: they are one independent party.
		ControlEdges: []ControlEdge{{From: "alpha-ca", To: "alpha-notary"}},
		Signatures: []Signature{
			{SignatureID: "sig-1", CertificateID: "cert-a1"},
			{SignatureID: "sig-2", CertificateID: "cert-a2"},
			{SignatureID: "sig-3", CertificateID: "cert-b1"},
			{SignatureID: "sig-4", CertificateID: "cert-g1"},
		},
		Thresholds: Thresholds{
			MinWitnesses:  3,
			MinRegions:    3,
			MinCategories: 2,
			MinPairs: map[string]int{
				"eu|ca": 1,
			},
		},
	}

	result, err := Solve(in)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	for _, w := range result.Witnesses {
		fmt.Printf("%s domain=%s region=%s category=%s\n",
			w.SignatureID, w.ControlDomain, w.Region, w.Category)
		// Output:
		// sig-1 domain=alpha-ca region=us category=ca
		// sig-3 domain=beta-ca region=eu category=ca
		// sig-4 domain=gamma-notary region=ap category=notary
	}
}
