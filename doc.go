// Package netsec contains reusable TLS security helpers.
//
// Its ticket age validator decodes TLS 1.3 obfuscated_ticket_age values and
// rejects tickets whose age exceeds their lifetime by more than the caller's
// configured tolerance. It can also compare the reported age with the age
// observed since the ticket was issued.
package netsec
