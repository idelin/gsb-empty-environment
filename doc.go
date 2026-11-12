// Package netsec implements request admission checks for an rsync daemon.
//
// A request is checked against the module's hosts allow/hosts deny lists,
// refused options, excluded paths, read-only setting, and filesystem root.
// Module lookup is exact and case-sensitive. Paths are interpreted as paths
// relative to the module root.
package netsec
