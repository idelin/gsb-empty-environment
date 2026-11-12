// Package netsec provides CAPWAP endpoint identity binding for a controller's
// Join handler.
//
// A Binder owns the controller's trusted certificate-to-WTP mappings. BindJoin
// verifies that the DTLS client certificate fingerprint resolves to the WTP
// identity, board serial number, and MAC reported in the join request, then
// creates a Binding keyed by certificate, WTP identity, CAPWAP session ID, and
// DTLS connection.
//
// A Binding accepts only control records that the DTLS layer marked
// authenticated and whose sequence number is inside the replay window. A
// fresh authenticated record may migrate the endpoint to a new source
// address; the previous address is retired immediately and all later
// datagrams from it are dropped.
package netsec
