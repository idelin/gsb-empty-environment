// Package netsec screens request-body bytes that arrive before an HTTP gateway
// sends a 100 Continue response.
//
// When a client sends Expect: 100-continue, it may pipeline another request
// into the first body segment. A gateway should call Decode (or use a Decoder)
// on the already-available body prefix before sending 100 Continue. If a
// complete HTTP request line appears, the decoder returns ErrSmuggledRequest.
package netsec
