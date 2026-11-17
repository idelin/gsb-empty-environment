// Package netsec provides versioned object releases whose content digest and
// security label are committed atomically.
//
// A Store never exposes content and label through separate reads. Publish
// stages the complete release and swaps it into place in one transaction, and
// every Handle returned by Get or Acquire is an immutable copy of that
// release. Missing labels are denied by default. Lowering a release's label
// level requires the caller to approve the exact current version.
package netsec
