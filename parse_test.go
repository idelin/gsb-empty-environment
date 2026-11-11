package ocspcheck

import (
	"encoding/asn1"
	"testing"
	"time"
)

// TestMalformedResponses ensures the parser rejects garbage and non-basic
// response types rather than trusting unparseable staples.
func TestMalformedResponses(t *testing.T) {
	p := newTestPKI(fixedNow)

	cfg := Config{}.withClock(func() time.Time { return fixedNow })

	t.Run("garbage bytes rejected", func(t *testing.T) {
		// Enumerated value 99 is not a defined OCSP response status.
		state := newConnectionState(p, marshalEnvelopeStatus(99))
		if err := cfg.checkAt(state, fixedNow); err == nil {
			t.Fatal("expected parse error for garbage envelope")
		}
	})

	t.Run("tryLater responder status rejected", func(t *testing.T) {
		der := marshalEnvelopeStatus(statusTryLater)
		state := newConnectionState(p, der)
		if err := cfg.checkAt(state, fixedNow); err == nil {
			t.Fatal("expected error for non-successful responder status")
		}
	})

	t.Run("wrong response type rejected", func(t *testing.T) {
		in := defaultResponseInput(p, p.caCert, p.caKey)
		in.responseType = asn1.ObjectIdentifier{1, 2, 3, 4, 5}
		wrong := buildOCSPResponse(in)
		state := newConnectionState(p, wrong)
		if err := cfg.checkAt(state, fixedNow); err == nil {
			t.Fatal("expected error for non-basic response type")
		}
	})

	t.Run("truncated DER rejected", func(t *testing.T) {
		good := buildOCSPResponse(defaultResponseInput(p, p.caCert, p.caKey))
		state := newConnectionState(p, good[:len(good)-5])
		if err := cfg.checkAt(state, fixedNow); err == nil {
			t.Fatal("expected error for truncated response")
		}
	})
}

func marshalEnvelopeStatus(status int) []byte {
	der, err := asn1.Marshal(ocspResponse{Status: asn1.Enumerated(status)})
	if err != nil {
		panic(err)
	}
	return der
}
