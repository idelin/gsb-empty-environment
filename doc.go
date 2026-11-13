// Package netsec provides TLS pre-shared-key identity selection checks.
//
// ValidateSelectedIdentity verifies that a server's selected_identity names an
// identity actually offered by the client. It rejects every index outside the
// client-provided identity list, including index zero when that list is empty,
// and it keeps index zero on the binder-check path when the list is non-empty.
package netsec
