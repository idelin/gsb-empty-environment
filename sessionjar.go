// Package sessionjar provides an http.CookieJar that partitions cookies by
// registrable site and session generation.
//
// Sites are isolated from each other: a cookie issued by site-a.test is never
// sent to site-b.test, including across cross-site redirects. Within a site,
// every call to Refresh starts a new generation with a fresh cookie store, so
// a login renewal cannot mix old and new session cookies.
//
// For race-free renewals while requests are in flight, wrap the client
// transport in GenerationTransport. It pins every request (and its redirect
// chain) to the generation that was current when the request started, so a
// Set-Cookie arriving late on an old-generation response is dropped instead of
// polluting the new generation.
package sessionjar
