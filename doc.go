// Package netsec provides a job-attribute and document-binding component for
// Go print services.
//
// A job is bound at submission time to an authenticated submitter, a queue,
// the canonical URI of its own isolated storage, and a SHA-256 content digest.
// Print attributes are accepted only through an exact-name whitelist. During
// print, cancel and reprint, any caller-supplied binding attribute must agree
// with the stored record; the stored record is always used to locate the
// document, so replacing job-uri or queue attributes cannot redirect a print
// to another user's document.
//
// See Service and NewFileService for the main integration points.
package netsec
