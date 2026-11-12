// Package pdfscan performs structural inspection of PDF documents for an
// attachment gateway. It never renders pages or executes document content.
//
// The scanner reconstructs the effective object graph, including objects
// hidden in Flate-compressed object streams and objects superseded by
// incremental updates, and rejects documents whose catalog or page tree
// can reach automatic actions, Launch actions, JavaScript or form
// submissions. Every finding reports the owning indirect object number.
//
// Name escapes of the form #xx are normalized during tokenization, so
// obfuscated names such as /Open#41ction or /#4cA#75nch are matched just
// like their plain forms.
package pdfscan
