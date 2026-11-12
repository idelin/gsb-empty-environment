// Package netsec parses and authorizes Netnews control messages for a POST
// handler. It rejects folded-header Control injection, verifies RFC 8315
// Cancel-Key/Cancel-Lock pairs, restricts unlocked originals to configured
// administrators, and detects Path loopbacks to the local site.
package netsec
