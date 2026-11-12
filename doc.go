// Package netsec implements destination-constrained SSH agent forwarding.
//
// A signing key is registered together with one or more complete
// source-to-destination host-key chains. Each forwarded agent connection
// records signed session-bind@openssh.com hops. Before a private-key
// signature is produced, the agent checks the full observed path, the
// destination username, and the session identifier in the SSH userauth
// request. Duplicate hops, missing bindings, loops, and unregistered
// destinations are rejected without invoking the signing callback.
package netsec
