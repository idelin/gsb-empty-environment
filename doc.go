// Package netsec verifies RFC 5652 Cryptographic Message Syntax (CMS)
// SignedData structures for the archive import pipeline.
//
// The verifier implements the mandatory signer checks from RFC 5652
// section 5.3 through 5.5:
//
//   - when signedAttrs is present it MUST contain the content-type and
//     message-digest attributes, each exactly once;
//   - the signature is computed over the DER encoding of signedAttrs with
//     its IMPLICIT [0] tag (0xA0) replaced by the universal SET tag
//     (0x31);
//   - the content-type attribute value MUST equal the encapsulated content
//     type (eContentType);
//   - the message-digest attribute value MUST equal the digest of the
//     encapsulated content.
//
// These checks prevent the two attacks called out in the archive import
// threat model: appending unsigned content that is then treated as signed,
// and accepting a signerInfo whose signedAttrs omit message-digest.
//
// The primary entry point is VerifySignedData; VerifySignedDataWithRoots
// additionally validates the certificate chain against a trust pool.
package netsec
