// Package netsec provides a ZIP reader that validates ZIP64 size metadata.
//
// A local header's 32-bit size fields are treated as hints. When a ZIP64
// extended information extra field is present, its 64-bit uncompressed and
// compressed sizes are authoritative. The effective local sizes must also
// agree with the central directory; any disagreement is rejected as ErrFormat
// before the archive is returned.
package netsec
