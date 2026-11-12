// Package netsec provides an embeddable DOCTYPE pre-validation guard for
// XML ingestion paths such as a SAML metadata importer.
//
// The guard scans only the XML prolog and the DOCTYPE's internal subset,
// classifying comments, processing instructions and entity declarations
// without ever invoking an XML parser or an entity resolver. A document is
// rejected when it contains:
//
//   - an external entity (a SYSTEM or PUBLIC external identifier),
//   - a parameter entity (the "%" keyword or a "%name;" reference),
//   - an internal entity whose expanded content grows past the caller
//     supplied expansion budget, or recursive entity references,
//   - malformed or unsupported markup declarations.
//
// Documents without a DOCTYPE are returned untouched, and no entity
// resolution callback is ever invoked for them. All scanning is purely
// syntactic: the guard never opens a file, a network connection or an
// XML parser.
package netsec
