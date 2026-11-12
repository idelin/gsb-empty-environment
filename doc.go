// Package netsec implements strict IMA extended-attribute appraisal for
// integrity engines that must authorize a path before exec or mmap.
//
// Attribute layout (signature v2):
//
//	[0]   type (0x01 digest, 0x03 signature)
//	[1]   version (must be 2)
//	[2]   hash/signature algorithm
//	[3:7] key id, big endian
//	[7:9] payload length, big endian
//
// ParseAttribute rejects truncated headers, wrong versions, unknown types and
// algorithms, and any mismatch between the declared and actual payload length.
// Policy decides whether a digest-only attribute is sufficient for a path and
// which key ids are trusted.
package netsec
