package warcwriter

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// httpEntityBody returns the decoded HTTP message entity body from a raw
// wire message. Transfer encodings such as chunked framing are removed, so
// the result is the actual payload that WARC-Payload-Digest must cover.
func httpEntityBody(raw []byte, isRequest bool) ([]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	if isRequest {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return nil, fmt.Errorf("warcwriter: parse request entity: %w", err)
		}
		if req.Body == nil {
			return nil, nil
		}
		defer req.Body.Close()
		return io.ReadAll(req.Body)
	}
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, fmt.Errorf("warcwriter: parse response entity: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
