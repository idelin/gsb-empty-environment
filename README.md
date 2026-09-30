# pseudonymizer

Domain-separated pseudonymization built on **standard HMAC-SHA256** (Python
standard library only), with versioned key rotation, atomic publication of
pseudonyms and their indexes, deterministic collision resolution, and
consistency/recovery checks. Python 3.11+, stdlib + `unittest` only.

**Scope: synthetic data only.** No real personal data is used in code, tests,
or examples.

## Definitions

- **Subject** — the entity to be pseudonymized (synthetic identifiers only).
- **Domain** — an isolation context. The same subject gets *different*
  pseudonyms in different domains; the same `(domain, subject)` pair always
  gets the *same* pseudonym under the same key version.
- **Pseudonym** — `truncate(HMAC-SHA256(key, INFO || 0x00 || domain || 0x00 ||
  subject || 0x00 || counter), pid_length)`, hex-encoded.
- **Collision** — two distinct subjects in one domain mapping to the same
  pseudonym (possible only because of truncation).
- **Rotation** — replacing the active key version; all pseudonyms are
  re-derived, and an explicit old→new link table is maintained.

## Parameters

| Parameter | Default | Notes |
|---|---|---|
| PRF | HMAC-SHA256 (`hmac`, `hashlib`) | standard library only; no custom crypto |
| key | caller-provided, ≥ 16 bytes (32 recommended) | held in memory, never logged |
| `pid_length` | 16 bytes (128 bits) | range 8–32 bytes |
| `counter` | 0 | deterministic collision-resolution retry index |
| key versions | caller-named (`"v1"`, `"v2"`, …) | duplicates rejected |

## Guarantees and non-guarantees

- **Deterministic**: same `(key version, domain, subject)` → same pseudonym.
- **Domain-separated**: domains do not share pseudonyms for the same subject.
- **Rotation**: old↔new pseudonyms are explicitly linkable via
  `resolve_link`, but old lookups never mix versions.
- **Atomicity**: each mutation builds a full successor state and publishes it
  with a single swap under one lock — a new pseudonym and all of its indexes
  (reverse owner map, collision counters, reference index, rotation links)
  become visible together.
- **Hygiene**: keys are never logged (redacted `repr`s); raw subjects never
  appear in the audit log (forbidden field names are rejected outright).
  Reference ids are caller-controlled external ids — callers must not
  embed raw subject identifiers in them.
- **Non-guarantee**: this is deterministic pseudonymization, **not**
  anonymization. Linkability within a `(key version, domain)` pair is by
  design. Any statistical observations (e.g. birthday-bound collision
  probability ≈ 2^(−n/2) for an n-bit pseudonym) are illustrative
  experiments, **not a formal privacy proof**.

## Collision handling

Detection uses the reverse owner map. Resolution is deterministic: the
derivation counter is incremented until a free pseudonym is found, and the
counter is persisted so the outcome is reproducible across restarts and
rotations. References are indexed by pseudonym and migrated atomically during
rotation, so no reference dangles after resolution or rotation. Collisions
can be injected for testing by passing a custom `derive_fn` to
`PseudonymStore` (production code uses the standard HMAC derivation).

## Concurrency, cancellation, recovery

- Concurrent requests for the same subject are idempotent — all callers
  observe the same pseudonym.
- Cancellation is cooperative via `threading.Event`; a cancelled operation
  raises `OperationCancelled` **before** commit and leaves the store (and,
  for rotation, the key store) unchanged.
- `check_consistency()` is the recovery check: assignment/owner duality, no
  dangling references, valid link targets. It raises `ConsistencyError` with
  the list of violations.

## Layout

- `pseudonymizer/crypto.py` — HMAC-SHA256 derivation (the only crypto)
- `pseudonymizer/keystore.py` — versioned caller-provided keys, redacted reprs
- `pseudonymizer/store.py` — atomic state publication, rotation, refs, checks
- `pseudonymizer/audit.py` — pseudonym-only audit log
- `tests/test_pseudonymizer.py` — unittest suite
- `demo.py` — runnable sample

## Run

```sh
python3 demo.py
python3 -m unittest discover -s tests -v
```
