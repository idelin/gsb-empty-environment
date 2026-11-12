// Package netsec evaluates ordered macOS Seatbelt-style sandbox rules and
// detects deny rules that later allows completely override.
//
// Evaluation starts from the implicit "(deny default)" policy. Rules are
// examined from first to last and the last matching rule wins. Operations
// ending in "*" match by prefix; literal, subpath, and whole-path regular
// expression filters select paths.
package netsec
