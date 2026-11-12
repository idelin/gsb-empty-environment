package netsec_test

import (
	"net/http"
	"testing"

	netsec "example.org/netsec/q0313"
)

func TestPublicAPI(t *testing.T) {
	t.Parallel()

	netrc, err := netsec.ParseNetrcString("machine example.com login alice password secret")
	if err != nil {
		t.Fatal(err)
	}
	transport := netsec.NewRoundTripperFromNetrc(netrc, http.DefaultTransport, false)
	var _ http.RoundTripper = transport
}
