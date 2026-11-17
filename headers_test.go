package netsec

import (
	"errors"
	"testing"
)

func TestPromisedRequestValidation(t *testing.T) {
	valid := []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: "a.example:443"},
		{Name: ":path", Value: "/asset.js?version=1"},
		{Name: "accept", Value: "*/*"},
		{Name: "authorization", Value: "Bearer other-source"},
		{Name: "cookie", Value: "session=other-source"},
	}
	request, err := parsePromisedRequest(valid)
	if err != nil {
		t.Fatalf("valid promise rejected: %v", err)
	}
	if request.Source.String() != "https://a.example" {
		t.Fatalf("source = %q", request.Source)
	}
	if request.Path != "/asset.js?version=1" {
		t.Fatalf("path = %q", request.Path)
	}
	if got := request.Headers.Get("Authorization"); got != "" {
		t.Fatalf("promise-supplied authorization leaked into request: %q", got)
	}
	if got := request.Headers.Get("Cookie"); got != "" {
		t.Fatalf("promise-supplied cookie leaked into request: %q", got)
	}

	tests := []struct {
		name   string
		fields []HeaderField
	}{
		{
			name: "missing path",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
			},
		},
		{
			name: "duplicate authority",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":authority", Value: "b.example"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "unsafe method",
			fields: []HeaderField{
				{Name: ":method", Value: "POST"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "pseudo after regular",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: "accept", Value: "*/*"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "uppercase pseudo name",
			fields: []HeaderField{
				{Name: ":Method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "connection specific",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
				{Name: "connection", Value: "keep-alive"},
			},
		},
		{
			name: "ambiguous host",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
				{Name: "host", Value: "b.example"},
			},
		},
		{
			name: "crlf injection",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
				{Name: "accept", Value: "*/*\r\nx-evil: 1"},
			},
		},
		{
			name: "authority userinfo",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "user@a.example"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "path not origin form",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "asset.js"},
			},
		},
		{
			name: "unknown pseudo header",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
				{Name: ":status", Value: "200"},
			},
		},
		{
			name: "te gzip",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "a.example"},
				{Name: ":path", Value: "/"},
				{Name: "te", Value: "gzip"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parsePromisedRequest(test.fields)
			if err == nil {
				t.Fatal("promise unexpectedly accepted")
			}
			if !errors.Is(err, ErrMalformedPromise) {
				t.Fatalf("error = %v, want ErrMalformedPromise", err)
			}
		})
	}
}
