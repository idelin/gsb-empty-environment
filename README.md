# SD-JWT disclosure verifier

Dependency-free TypeScript validation for SD-JWT disclosures and holder
key-binding JWTs.

## Install / import

The package is local and ESM-only:

```ts
import {
  verifySdJwt,
  verifySdJwtDisclosures,
  validateKeyBindingJwt,
} from "./index.ts";
```

## Disclosure validation

`verifySdJwtDisclosures(issuerPayload, disclosures, options?)` assumes that
`issuerPayload` has already passed issuer JWT signature verification. It
validates the complete disclosure set atomically and returns the reconstructed
claims only when every disclosure is valid.

```ts
const result = verifySdJwtDisclosures(issuerPayload, presentationDisclosures);

console.log(result.claims);       // _sd arrays removed
console.log(result.disclosures);  // decoded salt/name/value entries
```

Validation rules:

- Every disclosure is exactly `base64url(JSON.stringify([salt, name, value]))`.
- Its hash must occur exactly once in an authenticated `_sd` array.
- `_sd_alg` controls `sha-256`, `sha-384`, or `sha-512` for the whole tree.
- A nested disclosure is accepted only after its parent disclosure is expanded.
- Salts must be non-empty and unique among presented disclosures.
- Names `_sd` and `...` are reserved; plaintext name collisions are rejected.
- One invalid, duplicate, or unreachable disclosure rejects the whole input.

## Full presentation and KB-JWT

`verifySdJwt(presentation, options)` parses the compact issuer JWT, validates
all disclosures, and then validates an optional KB-JWT. Supply
`issuerPayload` (already signature-verified by the caller), or `issuerKey` /
`verifyIssuerJwt` to verify the issuer signature inside this module.

```ts
const result = verifySdJwt(sdJwtWithKb, {
  issuerPayload,
  expectedNonce: "server-nonce",
  expectedAudience: "https://verifier.example",
  now: Math.floor(Date.now() / 1000),
  holderKey, // optional when issuer payload cnf.jwk is available
  requireKbJwt: true,
});
```

The KB-JWT is checked against the exact SD-JWT serialization that was
presented:

- `sd_hash` must equal the `_sd_alg` digest of that string.
- `nonce` and `aud` must match the verifier's expected values.
- `exp` is mandatory and the token must not be expired.
- If a holder key, `cnf.jwk`, or `verifyKbJwt` callback is supplied, its
  signature is verified as well.

`validateKeyBindingJwt(kbJwt, sdJwtWithoutKb, options)` is exported for callers
that split the presentation themselves.

All failures throw `SdJwtValidationError` with a stable `code`, for example
`DISCLOSURE_NOT_SIGNED_OR_PARENT_WITHHELD`, `DUPLICATE_SALT`,
`ALGORITHM_MIXING`, or `KB_EXPIRED`.

## Tests

```sh
npm test
```
