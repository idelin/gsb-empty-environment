// Package netsec validates stanza source and destination addresses at an
// XMPP router entry point according to RFC 6120 section 8.
//
// Client streams must be bound before stanzas are accepted.  A missing from
// address is filled with the bound full JID, and a present from address must
// equal the bound full JID or its bare JID.  Server-to-server streams only
// accept from domains authenticated or validated on that stream, and stanzas
// whose to domain is not hosted locally are dropped.
package netsec
