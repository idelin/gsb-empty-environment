// Package netsec validates btrfs send streams before a receive service parses
// or applies them.
//
// ValidateStream checks the stream header, command framing, TLV bounds and
// CRC32C values, relative path safety, stream-created symlink traversal, and
// the expected SUBVOL/SNAPSHOT UUID relationship. Validation is streaming and
// does not buffer the whole stream.
package netsec
