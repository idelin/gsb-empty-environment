// Package netsec implements an anti-tamper scanner that hashes a live
// process image page by page. It never stitches pages from different
// modification generations into one accepted image: a digest tree is
// committed only when the image commit generation observed before and
// after the reads matches and every page is provably untouched since it
// was read. Otherwise only the affected pages are re-read, bounded by a
// repair budget.
package netsec
