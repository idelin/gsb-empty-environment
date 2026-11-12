// Package netsec provides admission checks for PostgreSQL extension
// installation, LOAD statements, and C-function shared-library references.
//
// The policy is intended for a database proxy: untrusted roles may install
// only trusted extensions whose complete requires closure is already
// installed and trusted, while shared-library names are restricted to
// "$libdir/<basename>".
package netsec
