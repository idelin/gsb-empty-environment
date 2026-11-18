// Package netsec provides admission control for user-supplied TOML 1.0.0
// fragments that are merged into a larger configuration.
//
// Every complete key path must start with the case-sensitive segment "user".
// Fragments are limited to 64 KiB. Use an Admitter to retain merge state across
// multiple fragments; Admit is atomic, so a rejected fragment does not change
// previously admitted state.
package netsec
