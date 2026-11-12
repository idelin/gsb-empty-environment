// Package netsec implements verification decisions for a build gateway that
// relies on Gradle-style verification-metadata.xml files.
//
// The package parses component checksums (including also-trust fallback
// checksums) and trusted-artifacts rules, flags over-broad or malformed
// trust rules, cross-checks parsed versions against a dependency lockfile,
// and decides whether a downloaded artifact is allowed to enter the build.
package netsec
