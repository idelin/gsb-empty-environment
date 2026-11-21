// Package redirectplan implements credential-safe HTTP redirects for download
// hosts.
//
// Credentials are bound to an exact, normalized origin and to the purpose of
// the request.  Every redirect hop is planned again from its normalized
// target; a credential is attached only when the caller configured one for
// that exact (origin, purpose) identity.  Redirect responses can contribute a
// Location, but never authentication headers or URL user information.
package redirectplan
