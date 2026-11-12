package netsec

import "testing"

func TestVerifySignedData_SubjectKeyIdentifierSID(t *testing.T) {
	for _, kind := range allKeyKinds() {
		t.Run(kindName(kind), func(t *testing.T) {
			tc := mustCert(t, kind)
			der, err := buildSignedData(tc, kind, testContent, builderOptions{useSKI: true})
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			certs, err := VerifySignedData(der, nil)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if len(certs) != 1 || !certs[0].Equal(tc.cert) {
				t.Fatal("signer certificate not resolved via SKI")
			}
		})
	}
}
