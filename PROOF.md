# Reuse authentication proof

`reuseauth` is an importable, dependency-free Go package for navigation-scoped
reuse of downloaded segments. The executable tests are the independent replay
proof; this document states the invariants and maps each requested scenario to
its witness.

## Binding statement

A minted proof binds:

```text
Ed25519_Authority_Sign(
  proofVersion, authorityID, requestID,
  navigationID, epoch,
  policySequence, policyHistoryPrefixDigest,
  requestContextDigest,
  signedRedirectChainDigest,
  signedFinalResponseDigest,
  manifestDigest, offeredRepresentationDigest,
  sharedRangeDigests, issuedAt)
```

Every digest uses a domain-separated, length-framed SHA-256 construction.
Range digests also bind the absolute byte range, so equal bytes at different
offsets are not treated as equivalent.

## Enforced invariants

- Signed redirect evidence: every hop is signed by a key trusted for that
  hop's URL origin; index, previous-hop digest, status, location, time window,
  responder origin, and redirect-to-next-URL linkage are verified.
- Signed final response: the terminal response signature, status, final URL,
  responder, audience, public flag, header digest, length, and manifest digest
  are bound to the terminal redirect hop.
- Same navigation generation: proof navigation ID and epoch must equal the
  current navigation. Revocation and top-level site switches advance the epoch.
- Same policy history: the proof binds the exact append-only history prefix.
  Verification additionally evaluates the latest matching current policy, so a
  post-prepare deny or revocation wins before cache commit.
- Credential/source alignment: context source, credential mode, and top-level
  site must equal the current navigation values; policy decisions are
  context-stamped with epoch, site, and credential mode.
- Content equivalence: sharing uses only exact `(absolute range, range-bound
  digest)` witnesses. A whole-segment digest is never used to infer an
  unknown subrange.
- Minimal visible change: `ChangeSet` adds only previously absent
  `range:digest` pairs, removes only disappeared ranges, coalesces removals,
  and reports length/completion/failure only on transitions. Epoch changes
  emit a reset/removal set for that navigation only.
- Late callback admission: `Broker.DeliverTo` checks receiver navigation,
  waiter ownership, exact recipient origin, presented/current epoch,
  revocation, signed public flag equality, private audience, current policy
  action, and proof revalidation before mutating visible state.
- Private-existence isolation: unauthorized private reuse returns an error
  satisfying `errors.Is(err, reuseauth.ErrNotFound)`; call
  `reuseauth.NavigationFacingError` before returning to navigation code.
  `DownloadID`, authority evidence, and `AuditEntry` values are not included in
  `ChangeSet` JSON.

## Scenario-to-proof map

Run from the repository root:

```sh
go test ./reuseauth -count=1 -v
go test -race ./reuseauth -count=1
go vet ./...
```

| Required replay/comparison | Executable witness |
| --- | --- |
| Small object, multiple signed redirects and segments | `TestFullReplayMultiSegmentRedirects` |
| Partial representation change shares only unchanged intervals | `TestFullReplayPartialRepresentationChange` |
| No inference from whole digest to partial range | `TestEquivalentRangesNeverInfersSubrange` |
| Minimal add/remove replacement set | `TestDiffRangesMinimalReplacement` |
| Waiter revocation fences completion, error, and length callbacks | `TestFullReplayWaiterRevocationFencesAllCallbacks` |
| Top-level site switch resets and requires new-epoch policy | `TestFullReplayTopLevelSwitchFencesLateLength` |
| All callback kinds enforce recipient origin and public flag | `TestAllCallbackKindsEnforceOriginAndPublicFlag` |
| Late length/origin/public-flag isolation and private lookup indistinguishability | `TestFullReplayLateLengthNotificationIsolation` |
| Policy revocation between cache prepare and commit | `TestFullReplayRevocationBeforeCacheHit` |
| Two executions differing only in hidden bytes | `TestHiddenDataDifferentialVisibleTraces` |
| Independent public-key proof verification and tamper rejection | `TestIndependentProofVerificationAndTampering` |
| Redirect status/location/signature tampering rejection | `TestSignedRedirectChainRejectsLinkTampering` |

## Differential proof

`TestHiddenDataDifferentialVisibleTraces` performs two complete deterministic
replays with identical public objects, identities, epochs, and operations.
Only the bytes/digests of an authorized owner-only object change.

- The unprivileged navigation's complete `[]ChangeSet` JSON and
  `TraceHash` are byte-identical across executions.
- The owner's trace changes, demonstrating that the hidden input was actually
  varied and remains cryptographically visible to the authorized recipient.

## Trust boundary

The host must supply the browser-canonical recipient origin and top-level site
key. This package does not perform DNS, network fetches, or eTLD+1 conversion.
`Broker.Audit` and `DeliveryReceipt` values are trusted-host metadata and must
not be forwarded to navigation script.
