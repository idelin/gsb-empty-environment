// Package netsec verifies signify-signed SHA256 manifests and checks
// completed downloads against the authenticated names in those manifests.
//
// A Verifier is bound to one caller-supplied public key. Signature comments
// are ignored, the embedded manifest body is authenticated before parsing,
// and download names must be safe basenames present in the manifest.
package netsec
