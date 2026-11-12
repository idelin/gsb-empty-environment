import { computeEventId, safeHexEqual } from './hash.ts';
import type {
  Event,
  SchnorrVerifier,
  Sha256Fn,
  ValidationFailure,
  ValidationResult,
} from './types.ts';

export interface ValidateOptions {
  /**
   * Injected BIP-340 Schnorr verifier. Required; this package has no crypto
   * dependency for signature checking on purpose.
   */
  verifySchnorr: SchnorrVerifier;
  /**
   * Maximum number of seconds `created_at` may be ahead of `now`. The check
   * is `created_at > now + futureLimitSeconds`. Defaults to 0 (no future
   * events at all).
   */
  futureLimitSeconds?: number;
  /** Epoch seconds to validate against; injectable for tests. Defaults to Date.now()/1000. */
  now?: number;
  /** Injectable SHA-256 (defaults to node:crypto). */
  sha256?: Sha256Fn;
}

const LOWER_HEX_64 = /^[0-9a-f]{64}$/;
const LOWER_HEX_128 = /^[0-9a-f]{128}$/;

function fail(
  code: ValidationFailure['code'],
  reason: string,
): ValidationFailure {
  return { ok: false, code, reason };
}

function isNonNegSafeInteger(value: unknown): value is number {
  return (
    typeof value === 'number' &&
    Number.isSafeInteger(value) &&
    value >= 0
  );
}

function isStringArray(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.every((element) => typeof element === 'string')
  );
}

/**
 * Full NIP-01 admission check for an event arriving at a relay.
 *
 * The id is always recomputed from a freshly produced canonical
 * serialization, never compared against bytes or an id supplied by the
 * client, so non-canonical JSON escape variants cannot forge a valid id.
 */
export async function validateEvent(
  raw: unknown,
  options: ValidateOptions,
): Promise<ValidationResult> {
  if (typeof options?.verifySchnorr !== 'function') {
    throw new TypeError(
      'validateEvent requires options.verifySchnorr: a Schnorr verifier must be injected by the caller',
    );
  }

  const futureLimitSeconds = options.futureLimitSeconds ?? 0;
  if (
    typeof futureLimitSeconds !== 'number' ||
    !Number.isFinite(futureLimitSeconds) ||
    futureLimitSeconds < 0
  ) {
    throw new TypeError(
      'futureLimitSeconds must be a finite non-negative number',
    );
  }

  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return fail('NOT_OBJECT', 'event must be a JSON object');
  }

  const candidate = raw as Record<string, unknown>;

  if (typeof candidate.pubkey !== 'string' || !LOWER_HEX_64.test(candidate.pubkey)) {
    return fail('BAD_PUBKEY', 'pubkey must be 64 lowercase hex characters');
  }
  if (!isNonNegSafeInteger(candidate.created_at)) {
    return fail('BAD_CREATED_AT', 'created_at must be a non-negative safe integer');
  }
  if (!isNonNegSafeInteger(candidate.kind)) {
    return fail('BAD_KIND', 'kind must be a non-negative safe integer');
  }
  if (
    !Array.isArray(candidate.tags) ||
    !candidate.tags.every(isStringArray)
  ) {
    return fail('BAD_TAGS', 'tags must be an array of arrays of strings');
  }
  if (typeof candidate.content !== 'string') {
    return fail('BAD_CONTENT', 'content must be a string');
  }
  if (typeof candidate.id !== 'string' || !LOWER_HEX_64.test(candidate.id)) {
    return fail('BAD_ID', 'id must be 64 lowercase hex characters');
  }
  if (typeof candidate.sig !== 'string' || !LOWER_HEX_128.test(candidate.sig)) {
    return fail('BAD_SIG', 'sig must be 128 lowercase hex characters');
  }

  const event: Event = {
    id: candidate.id,
    pubkey: candidate.pubkey,
    created_at: candidate.created_at,
    kind: candidate.kind,
    tags: candidate.tags,
    content: candidate.content,
    sig: candidate.sig,
  };

  const expectedId = await computeEventId(event, options.sha256);
  if (!safeHexEqual(expectedId, event.id)) {
    return fail(
      'BAD_ID',
      'id does not match sha256 of the canonical event serialization',
    );
  }

  const now = options.now ?? Math.floor(Date.now() / 1000);
  if (event.created_at > now + futureLimitSeconds) {
    return fail(
      'FUTURE_TIMESTAMP',
      `created_at ${event.created_at} is more than ${futureLimitSeconds}s in the future (now=${now})`,
    );
  }

  let sigValid: boolean;
  try {
    sigValid = await options.verifySchnorr(event.id, event.pubkey, event.sig);
  } catch (error) {
    return fail(
      'SIG_VERIFIER_ERROR',
      `injected Schnorr verifier threw: ${(error as Error).message ?? String(error)}`,
    );
  }
  if (sigValid !== true) {
    return fail('BAD_SIG', 'Schnorr signature verification failed');
  }

  return { ok: true, event };
}

/** Throwing variant for callers that prefer exceptions over result objects. */
export async function assertValidEvent(
  raw: unknown,
  options: ValidateOptions,
): Promise<Event> {
  const result = await validateEvent(raw, options);
  if (!result.ok) {
    const error = new Error(`NIP-01 validation failed [${result.code}]: ${result.reason}`);
    (error as Error & { code?: string }).code = result.code;
    throw error;
  }
  return result.event;
}
