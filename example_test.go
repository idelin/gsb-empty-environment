package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0218"
)

func ExampleAssessViews() {
	body := []byte("Hello\r\n\r\n\r\n")
	l := int64(7)
	header := "DKIM-Signature: v=1; a=rsa-sha256; c=simple/simple; " +
		"d=example.com; s=sel; l=7; h=from; bh=YWJj; b=YWJj"

	receiver := netsec.NewBodyReceiver()
	_, _ = receiver.Write(body)
	views, _ := receiver.Finish()
	result, _ := netsec.AssessViews(views, []netsec.SignatureVerification{
		{Header: header, Verified: true},
	}, netsec.PolicyCompatibility)

	fmt.Println(result.Decision)
	fmt.Println(result.Signatures[0].CoveredCanonicalBytes == l)
	fmt.Println(result.Signatures[0].TailOnlyDiscarded)
	// Output:
	// authenticated
	// true
	// true
}
