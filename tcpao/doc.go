// Package tcpao implements the key-selection and authentication rules needed
// by a TCP-AO transport. It is deliberately independent of the TCP and BGP
// framing code so that a BGP transport can use the same selector for both
// inbound validation and outbound key selection.
//
// The selector is fail-closed. An inbound segment is usable only when its
// KeyID identifies a registered key, that key's receive validity covers the
// segment time, and the MAC verifies with that key. RNextKeyID is processed
// only after those checks succeed. A requested next key also has to be
// registered and within its send validity; otherwise the inbound segment may
// still be delivered, but the send key is not changed.
//
// Key IDs and RNextKeyIDs are bound into the MAC input. Consequently, an
// attacker cannot alter either option on a captured segment without invalidating
// its MAC.
package tcpao
