// Package netsec provides a call interception guard for a PKCS#11 proxy
// fronting an HSM. A Guard sits on the forwarding path and enforces
// non-exportability policy before every sensitive request reaches the
// trusted backend:
//
//   - GetAttributeValue never discloses CKA_VALUE (or other secret-bearing
//     attributes) of an object whose CKA_SENSITIVE attribute is true.
//   - WrapKey refuses to export objects whose CKA_EXTRACTABLE attribute is
//     false.
//   - CopyObject and SetAttributeValue cannot flip CKA_EXTRACTABLE from
//     false to true.
//
// Policy decisions read the object's current attributes from the backend,
// so session objects receive exactly the same protection as token objects.
// Every decision fails closed: if a required boolean attribute cannot be
// read or decoded, the call is denied rather than forwarded.
package netsec
