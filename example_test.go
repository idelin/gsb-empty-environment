package netsec_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	netsec "example.org/netsec/q0355"
)

// verifier is a stand-in for the gateway's real XML-DSIG stack.
func verifier(_ context.Context, _ []byte) (*netsec.SignatureResult, error) {
	return &netsec.SignatureResult{SignedReferences: []string{"#TS-1", "#Body-1"}}, nil
}

func ExampleValidator() {
	validator := netsec.New(verifier, netsec.NewMemoryNonceStore())

	now := time.Now().UTC()
	message := `<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"
               xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"
               xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
  <soap:Header>
    <wsse:Security>
      <wsu:Timestamp wsu:Id="TS-1">
        <wsu:Created>` + now.Format(time.RFC3339) + `</wsu:Created>
        <wsu:Expires>` + now.Add(5*time.Minute).Format(time.RFC3339) + `</wsu:Expires>
      </wsu:Timestamp>
      <wsse:UsernameToken wsu:Id="UT-1">
        <wsse:Username>gw-user</wsse:Username>
        <wsse:Nonce>unique-nonce-value</wsse:Nonce>
        <wsu:Created>` + now.Format(time.RFC3339) + `</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </soap:Header>
  <soap:Body wsu:Id="Body-1"><trade/></soap:Body>
</soap:Envelope>`

	err := validator.Validate(context.Background(), strings.NewReader(message))
	fmt.Println("first message accepted:", err == nil)

	err = validator.Validate(context.Background(), strings.NewReader(message))
	fmt.Println("replay rejected:", errors.Is(err, netsec.ErrReplay))
	// Output:
	// first message accepted: true
	// replay rejected: true
}
