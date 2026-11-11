// Package pdfactions implements revision-bound, safe extraction of the page
// actions carried by a PDF document for a previewer frontend.
//
// The parser understands the subset of PDF syntax needed to follow indirect
// objects and the page tree without any third-party dependencies. Every
// resolution performed while walking a [Revision] is bound to that revision's
// cross-reference table ("the same generation of xref"): an old revision never
// reads the body written by a later incremental update, and parsed results are
// cached per revision so a newer action can never be served from an older
// revision's cache.
//
// By default only in-document navigation (GoTo and a small allowlist of named
// page actions) is delivered to the frontend. JavaScript, launch/external
// program actions and every other action type are intercepted and reported as
// blocked events instead of being executed.
package pdfactions
