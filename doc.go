// Package netsec builds closure signature manifests for Go release hosts
// and serves release objects through a loader restricted to manifest entries.
//
// A release package ships executables plus runtime data; signing only the
// main binary leaves every other file as a replacement entry point. The
// host describes the release as a dependency Graph whose edges are
// required, optional, or platform-selected, then calls Build to compute
// every object actually readable under the current configuration. The
// resulting Manifest binds each object's canonical relative path, file
// mode, and SHA-256 digest; its canonical encoding is what the host signs.
//
// Loader is the independent consumer side: it refuses to read anything not
// listed in the manifest, rejects non-canonical path aliases, and
// re-verifies mode and digest on every read.
package netsec
