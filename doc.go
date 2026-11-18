// Package netsec translates Tcl safe-interpreter virtual paths such as
// "$p(:0:)/scripts/init.tcl" into confined host filesystem operations.
//
// The Translator keeps directory handles open for every configured access
// root and allowed subdirectory. Open, OpenSource, Source, and Glob use those
// handles rather than reopening a translated string, preventing a directory
// replacement between validation and use from redirecting access outside the
// configured tree. Errors contain only canonical virtual tokens, never real
// access-root prefixes.
package netsec
