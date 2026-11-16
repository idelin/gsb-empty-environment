// Package netsec validates OpenSSH host key update notifications and applies
// verified changes to known_hosts.
//
// A hostkeys-00@openssh.com notification is untrusted input. Handle refuses to
// process one unless the caller has completed host authentication and
// known_hosts already contains an entry for the host. Every new key must have
// a valid hostkeys-prove-00@openssh.com signature over the session identifier
// and public key. The complete batch is validated before known_hosts is
// replaced atomically, so one bad proof cannot partially update the file.
package netsec
