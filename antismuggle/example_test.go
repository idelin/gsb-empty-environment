package antismuggle_test

import (
	"fmt"
	"io"
	"strings"

	"formal.local/w28-0dca105ae6/antismuggle"
)

func Example() {
	stream := strings.NewReader(
		"POST /ingest HTTP/1.1\r\n" +
			"Host: api.example\r\n" +
			"Transfer-Encoding:chunked\r\n" +
			"\r\n" +
			"5\r\nhello\r\n" +
			"0\r\n\r\n",
	)

	cfg := antismuggle.DefaultConfig()
	cfg.AllowedTrailers = []string{"x-trace"}
	conn := antismuggle.NewConn(stream, cfg)

	req, err := conn.Next()
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s %s %s %v\n", req.Method, req.Target, req.Version, req.Transfer)

	normalized, err := antismuggle.Normalize(req)
	if err != nil {
		panic(err)
	}
	fmt.Printf("body=%q\n", normalized.Body)
	fmt.Printf("wire=%q\n", normalized.Bytes())

	if _, err := conn.Next(); err != io.EOF {
		panic(err)
	}
	// Output:
	// POST /ingest HTTP/1.1 chunked
	// body="hello"
	// wire="POST /ingest HTTP/1.1\r\nHost: api.example\r\nContent-Length: 5\r\n\r\nhello"
}
