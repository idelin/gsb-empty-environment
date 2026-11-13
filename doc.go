// Package netsec parses and evaluates Android network-security-config XML at
// build time.
//
// Evaluation resolves the most specific matching domain-config, inherits
// trust anchors and pins from enclosing configs, applies targetSdk-dependent
// defaults, ignores expired pin sets, and applies debug-overrides only to
// debuggable builds.
package netsec
