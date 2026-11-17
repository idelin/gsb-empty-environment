// Package netsec plans authorization for download requests that follow
// redirects.
//
// Credentials are bound to an exact normalized origin and an application
// purpose. Every redirect hop starts from a request with credential-bearing
// headers removed, then reapplies only the credential configured by the caller
// for the normalized destination. URL userinfo, response headers, and headers
// inherited from the previous hop cannot authorize a new destination.
package netsec
