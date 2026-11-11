// Package sshchannel multiplexes independently authorized logical channels
// over a single session byte stream (for example, an SSH jump-host session).
//
// A jump host may permit interactive login without permitting arbitrary port
// forwarding. This package makes every open request pass its own
// authorization and pins the granted target before any dial starts. Dials run
// asynchronously, but each channel re-validates its grant after the dial
// returns and before the channel is admitted, so a revocation that races an
// in-flight dial cannot be bypassed. Channel numbers are reusable: a channel
// is identified by both its number and an epoch, ensuring a late dial
// callback from a superseded channel can never take over the recycled number.
package sshchannel
