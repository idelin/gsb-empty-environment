package handoff

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCertificateJSONRoundTripAndStatementSignature(t *testing.T) {
	plan, err := BuildSafePlan(testSpec("e1", "e2"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := IssueCertificate(plan.Protocol, VerifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cert)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Certificate
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Root != cert.Root || decoded.ProtocolDigest != cert.ProtocolDigest {
		t.Fatal("JSON round trip lost digest commitment")
	}
	if _, err := VerifyCertificate(&decoded, plan.Protocol, VerifyOptions{}); err != nil {
		t.Fatalf("decoded certificate verify: %v", err)
	}

	pub, priv, err := GenerateControllerKey()
	if err != nil {
		t.Fatal(err)
	}
	signed := SignStatement(priv, plan.PrepositionStatement)
	if err := signed.Verify(pub); err != nil {
		t.Fatal(err)
	}
	wrongPub, _, err := GenerateControllerKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := signed.Verify(wrongPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("wrong key err=%v, want ErrBadSignature", err)
	}
}
