// Package netsec validates RubyGems archives for a mirror service.
//
// Validation is deliberately independent of extraction: the outer gem tar is
// read exactly once, duplicate member names are rejected, signatures are
// checked against the same in-memory bytes that checksums.yaml.gz describes,
// and SHA-512 values are compared with those bytes. This avoids the classic
// "two members with the same name" verification/extraction split.
//
// HighSecurity requires valid RSA-SHA256 signatures on metadata.gz and
// data.tar.gz whose certificate chains terminate at a caller-supplied trust
// root. MediumSecurity verifies signatures when present but permits a wholly
// unsigned gem; a partial signature set is rejected at both levels.
package netsec
