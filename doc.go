// Package netsec implements security policy checks for a Go PKCS #11 access
// layer. It validates key attribute snapshots before creation and before
// updates, including irreversible sensitivity/extractability changes, and
// checks CKA_WRAP_WITH_TRUSTED restrictions during wrapping operations.
package netsec
