// Package netsec assembles hermetic build-input views from a manifest for
// remote build hosts. Manifest entries declare paths, link targets, and
// file digests; links resolve relative to their containing directory,
// may only name manifest objects (never host paths), and are rejected if
// they escape the input root or form cycles.
package netsec
