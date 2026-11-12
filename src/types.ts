/**
 * A syntactically valid NIP-01 event. `validateEvent` returns this narrowed
 * shape after every field has been checked and the id and signature verified.
 */
export interface Event {
  id: string;
  pubkey: string;
  created_at: number;
  kind: number;
  tags: string[][];
  content: string;
  sig: string;
}

/**
 * The fixed six-element tuple that NIP-01 hashes to derive an event id:
 * `[0, pubkey, created_at, kind, tags, content]`.
 */
export type CanonicalEventTuple = readonly [
  0,
  string,
  number,
  number,
  ReadonlyArray<ReadonlyArray<string>>,
  string,
];

/**
 * Injected Schnorr (BIP-340) verifier. The relay supplies its own
 * implementation; this package never performs elliptic-curve work itself.
 * Returning `false` (or resolving to it) means the signature is invalid.
 * A thrown error is reported as `SIG_VERIFIER_ERROR` rather than `BAD_SIG`.
 */
export type SchnorrVerifier = (
  id: string,
  pubkey: string,
  sig: string,
) => boolean | Promise<boolean>;

/** Injectable SHA-256; defaults to a synchronous `node:crypto` implementation. */
export type Sha256Fn = (
  data: Uint8Array,
) => Uint8Array | Promise<Uint8Array>;

export type ValidationErrorCode =
  | 'NOT_OBJECT'
  | 'BAD_PUBKEY'
  | 'BAD_CREATED_AT'
  | 'BAD_KIND'
  | 'BAD_TAGS'
  | 'BAD_CONTENT'
  | 'BAD_ID'
  | 'FUTURE_TIMESTAMP'
  | 'BAD_SIG'
  | 'SIG_VERIFIER_ERROR';

export interface ValidationFailure {
  ok: false;
  code: ValidationErrorCode;
  reason: string;
}

export interface ValidationSuccess {
  ok: true;
  event: Event;
}

export type ValidationResult = ValidationSuccess | ValidationFailure;

/** How a relay should treat an event kind regarding persistence/replacement. */
export type ReplaceabilityClass =
  | 'regular'
  | 'replaceable'
  | 'ephemeral'
  | 'parameterized';
