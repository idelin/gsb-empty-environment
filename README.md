# Strict DER certificate component

`netsec` is a C++20 library for certificate consumers that must not disagree
about what a certificate says.  It has one DER decoder and one canonical tree
per input.  The validator and the business layer should consume the identity
returned by that same `Certificate` object.

## Guarantees

- Only DER is accepted.  Indefinite lengths, non-minimal long-form lengths,
  leading-zero length fields, truncated values and trailing bytes are rejected.
- Identifiers are canonical.  High-tag-number form is rejected for tags below
  31, primitive/constructed forms must match the universal type, and
  end-of-contents is always rejected.
- Primitive values are canonical.  BOOLEAN is `00`/`ff`, INTEGER/ENUMERATED
  have no redundant sign octets, BIT STRING padding is zero and counted
  correctly, NULL is empty, and OBJECT IDENTIFIER sub-identifiers have no
  leading zero base-128 group.
- Universal `SET OF` values are in DER order, and DER times use the canonical
  `Z` forms.
- `Certificate::parse` builds one tree and extracts subject, SAN,
  basicConstraints and keyUsage from it.  Duplicate extension OIDs are
  rejected, including duplicates that carry different SAN values.
- `subject`, `issuer`, SAN, basicConstraints and keyUsage all come from the
  same parse.  `Certificate::identity()` is a copied, length-delimited snapshot
  suitable for business decisions.
- Signature verification receives the exact original `TBSCertificate` slice,
  not a re-encoding.  The built-in RSA verifier also parses
  SubjectPublicKeyInfo with `StrictDer`, so there is no second decoding path.
- Bounded recursion, node count and value size prevent adversarial nesting
  from exhausting the service.

## API

```cpp
#include "netsec.hpp"

const netsec::Bytes der = load_der_certificate();

try {
  const netsec::Certificate cert = netsec::Certificate::parse(der);

  const netsec::CertificateIdentity identity = cert.identity();
  const bool signature_ok = cert.verify_signature();

  if (!signature_ok) {
    reject("invalid signature");
  }

  const netsec::Name& subject = cert.subject();
  for (const netsec::GeneralName& san : cert.subject_alt_names()) {
    use(san.kind, san.value);
  }
  const netsec::BasicConstraints& bc = cert.basic_constraints();
  const netsec::KeyUsage& ku = cert.key_usage();
} catch (const netsec::DerError& error) {
  reject(netsec::to_string(error.code()), error.offset(), error.what());
}
```

`Certificate::parse` throws `DerError` on any encoding or structural violation.
The error carries a stable `DerErrorCode` and the byte offset in the input.
`DerLimits` can tighten or relax the default depth, node and value-size bounds.

The built-in `RsaPkcs1Verifier` supports SHA-1, SHA-256, SHA-384 and SHA-512
PKCS#1 v1.5 signatures.  `verify_signature()` uses the certificate's own
SubjectPublicKeyInfo; `verify_signature_with_key()` takes the issuer's SPKI for
path validation.  Other algorithms can be supplied through `SignatureVerifier`;
the request contains the raw TBS, raw signature, raw SubjectPublicKeyInfo,
algorithm OID and parameters.  A custom verifier must hash `request.tbs`
directly and must not re-encode it.

## Build and test

```sh
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build
ctest --test-dir build --output-on-failure
```

The tests use an embedded valid v3 certificate plus adversarial mutations:
indefinite outer/TBS lengths, leading-zero and non-minimal lengths, trailing
data, non-canonical tags and INTEGERs, duplicate SAN extensions with two
different identities, and a raw-TBS tamper that still parses but fails
verification.
