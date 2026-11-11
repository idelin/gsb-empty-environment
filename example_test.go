package hartransport_test

import (
	"bytes"
	"fmt"
	"net/http"

	"example.org/hartransport"
)

// Record real traffic through the recording RoundTripper and write HAR.
func ExampleRecorder() {
	rec := &hartransport.Recorder{
		Next:           http.DefaultTransport,
		CreatorName:    "my-crawler",
		CreatorVersion: "0.1.0",
	}
	client := &http.Client{Transport: rec}

	resp, err := client.Get("https://example.com/products")
	if err != nil {
		panic(err)
	}
	resp.Body.Close()

	var har bytes.Buffer
	if err := rec.WriteHAR(&har); err != nil {
		panic(err)
	}
	fmt.Println(har.Len(), "bytes of HAR written")
}

// Replay the recorded HAR in tests with no network access.
func ExampleNewReplayTransportFromReader() {
	// harData is a HAR 1.2 document previously produced by Recorder.
	var harData *bytes.Reader

	transport, err := hartransport.NewReplayTransportFromReader(harData)
	if err != nil {
		panic(err)
	}
	// Optionally restrict which headers participate in matching.
	transport.MatchHeaders = []string{"Content-Type", "Authorization"}

	client := &http.Client{Transport: transport}
	resp, err := client.Get("https://example.com/products")
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
}
