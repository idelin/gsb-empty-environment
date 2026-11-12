// Package netsec provides repository admission checks.
//
// The replace package surface in this module rejects history rewriting
// through Git replace refs and info/grafts. It operates only on the true
// commit graph supplied by the caller; replacement targets are never used
// to traverse history.
package netsec
