// Package netsec parses and verifies RFC 5652 CMS SignedData for file and
// archive import workflows. When signed attributes are present, it requires
// exactly one content-type and one message-digest attribute, checks that the
// content-type matches eContentType, verifies the digest against the actual
// content, and verifies the signature over the signed attributes encoded as a
// DER SET OF (tag 31) rather than the implicit [0] tag.
package netsec
