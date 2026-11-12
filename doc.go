// Package netsec composes page-level memory permissions from a loader's
// segment layout and reports the minimal segment sets responsible for
// dangerous writable-and-executable pages.
//
// A loader that maps several file segments onto shared pages cannot audit
// segments one by one: the effective permission of a page is the union of
// the permissions of every segment covering any byte of that page, with
// zero-filled (BSS) ranges counting as mapped just like file-backed ones.
// Analyze computes exactly that composition and reports where it turns
// dangerous.
package netsec
