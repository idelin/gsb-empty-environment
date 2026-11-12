// Package netsec provides request handling for reverse proxies that must
// safely process the HTTP Expect: 100-continue exchange.
//
// The handler validates ambiguous framing, enforces a declared and streamed
// request body budget, waits for an authentication decision before allowing a
// 100 response, strips Expect before forwarding, and closes connections when
// unread bodies or policy violations make reuse unsafe.
package netsec
