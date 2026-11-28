# WebAuthn L3 Assertion Verifier (zero runtime dependencies)

Importable TypeScript component for high-security relying parties. It performs
full `webauthn.get` assertion state validation, authenticator clone detection
and one-time challenge replay protection. The only platform dependency is the
built-in Node.js `crypto` module (the signature verifier can alternatively be
injected, e.g. for an HSM/KMS).

## Entry point

```ts
import {
  verifyAssertion,
  InMemoryChallengeStore,
  InMemoryCredentialStore,
  WebAuthnVerificationError,
} from "./src/index.ts";
```

## Verification pipeline

1. Parse raw `authenticatorData` (minimum 37 bytes, strict reserved-bit and
   CBOR length checks) and `clientDataJSON`.
2. Atomically consume the one-time `challenge` before any `await`, so
   concurrent requests can never replay it.
3. Verify `rpIdHash === SHA-256(rpId)`.
4. Require `type === "webauthn.get"`, exact `origin` equality (single value or
   allow-list) and reject `crossOrigin === true` unless explicitly allowed.
5. Require `UP=1`; require `UV=1` by default (policy toggle available).
6. Enforce backup flag invariants:
   - hard reject `BE=0 && BS=1`;
   - hard reject a registered `BE=false` credential ever asserting `BE=true`;
   - tenant policy may reject every `BE=1` synced passkey
     (`requireDeviceBoundCredential`).
7. Load the credential and refuse locked records before signature work.
8. Verify the signature over `authenticatorData || SHA-256(clientDataJSON)`:
   - ES256 (ECDSA P-256): strict DER (minimal lengths, no trailing bytes) plus
     mandatory low-S normalization;
   - EdDSA (Ed25519);
   - RS256 (RSA PKCS#1 v1.5 with SHA-256).
9. Clone detection per WebAuthn L3: when either the stored or observed counter
   is non-zero and `newSignCount <= storedSignCount`, the credential is locked
   and login is rejected. `0 -> 0` remains the spec-defined counter opt-out.
10. Persist the new `signCount` and backup flags only after every check passes.

## Minimal example

```ts
const credentialStore = new InMemoryCredentialStore();
const challengeStore = new InMemoryChallengeStore();

// At login initiation:
challengeStore.issue(challengeBytes, 5 * 60 * 1000);

// On assertion response:
try {
  const result = await verifyAssertion({
    assertion: {
      authenticatorData,
      clientDataJSON,
      signature,
      credentialId,
    },
    rpId: "example.com",
    expectedOrigin: "https://example.com",
    challenge: challengeStore,
    credentialStore,
    requireUserVerification: true,
    requireDeviceBoundCredential: true,
  });
  // result.signCount, result.backupEligible, result.backupState
} catch (error) {
  if (error instanceof WebAuthnVerificationError) {
    // Branch on the stable `error.code`, e.g. "CLONE_DETECTED".
  }
}
```

### Injecting an external verifier

```ts
await verifyAssertion({
  // ...
  signatureVerifier: {
    verify: ({ algorithm, publicKey, signedData, signature }) =>
      hsmClient.verify(algorithm, publicKey, signedData, signature),
  },
});
```

## Public key representations

`CredentialRecord.publicKey` accepts a JWK, SPKI DER bytes, or a Node
`KeyObject` wrapped as `{ keyObject }`. The algorithm is taken from the
credential record's COSE identifier (`-7` ES256, `-8` EdDSA, `-257` RS256)
and the key type is checked against it.

## Error model

All failures throw `WebAuthnVerificationError` subclasses carrying a stable
`code` (`CLONE_DETECTED`, `BACKUP_FLAGS_INVALID`,
`BACKUP_ELIGIBILITY_TRANSITION`, `DEVICE_BOUND_POLICY`, `CHALLENGE_REPLAY`,
`CHALLENGE_EXPIRED`, `CROSS_ORIGIN_FORBIDDEN`, `SIGNATURE_INVALID`, ...).
Clone and BE-transition failures persist `locked: true` plus
`lockedReason` on the credential record.

## Tests

```sh
npm test
```

The suite uses only `node:test`/`node:assert` and real generated key pairs. It
covers counter regression/stagnation lockout, uint32 wraparound, illegal
BE/BS combinations and transitions, device-bound policy, concurrent challenge
replay bursts, TTL expiry, cross-origin rejection, strict DER/low-S rules,
tampered signatures, injected verifiers and all three signature algorithms.
