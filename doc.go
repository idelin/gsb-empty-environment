// Package netsec provides DNS name-constraint matching for certificate
// validation paths.
//
// DNS name constraints are expressed without a leading dot. The match
// functions in this package accept a leading dot for compatibility but treat
// it as having no effect, so ".example.com" and "example.com" both match
// "example.com" and its subdomains.
package netsec
