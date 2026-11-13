// Package netsec analyzes the leakage of an encrypted keyword index.
//
// The model deliberately uses only first- and second-order leakage: the
// number of documents containing one keyword and the number of documents
// containing a pair of keywords. The unknown anonymous-document mapping is
// never guessed or used. Two hit sets with the same intersection sizes are
// therefore indistinguishable even when their underlying document groups
// differ.
package netsec
