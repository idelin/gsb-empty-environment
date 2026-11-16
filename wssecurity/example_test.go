package wssecurity_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"example.org/netsec/q0355/wssecurity"
)

// verifySignature is a stand-in for the gateway's real XML-DSig verification:
// it validates the signature and returns the ids covered by SignedInfo.
func verifySignature(_ context.Context, _ io.Reader) ([]string, bool, error) {
	return []string{"#TS-1", "#BODY-1"}, true, nil
}

func ExampleValidator() {
	validator, err := wssecurity.New(wssecurity.Config{
		VerifySignature: verifySignature,
		// NonceCache defaults to an in-memory cache; inject Redis in a fleet.
		Now: func() time.Time {
			return time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		fmt.Println("config error:", err)
		return
	}

	msg := strings.NewReader(`<soap:Envelope
  xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
  xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <soap:Header><wsse:Security>
    <wsu:Timestamp wsu:Id="TS-1">
      <wsu:Created>2026-11-17T01:59:55Z</wsu:Created>
      <wsu:Expires>2026-11-17T02:01:55Z</wsu:Expires>
    </wsu:Timestamp>
    <wsse:UsernameToken wsu:Id="UT-1">
      <wsse:Nonce>bm9uY2U=</wsse:Nonce>
      <wsu:Created>2026-11-17T01:59:55Z</wsu:Created>
    </wsse:UsernameToken>
  </wsse:Security></soap:Header>
  <soap:Body wsu:Id="BODY-1"><Echo/></soap:Body>
</soap:Envelope>`)

	if err := validator.Validate(context.Background(), msg); err != nil {
		fmt.Println("rejected:", err)
		return
	}
	fmt.Println("accepted")
	// Output: accepted
}
