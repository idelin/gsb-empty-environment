import { createHmac, timingSafeEqual } from 'node:crypto';
import { InvalidSyncTokenError } from './errors.ts';

const TOKEN_PREFIX = 'urn:carddav:sync:v1:';
const TOKEN_SCHEMA_VERSION = 1;
const MAX_TOKEN_LENGTH = 16_384;
const BASE64URL = /^[A-Za-z0-9_-]+$/;

export interface SyncTokenClaims {
  /** Token schema version, not the addressbook revision. */
  readonly tokenVersion: 1;
  readonly collectionId: string;
  readonly collectionPath: string;
  readonly principalId: string;
  /** Collection revision this token can synchronize from. */
  readonly revision: number;
}

export interface SyncTokenExpectation {
  readonly collectionId: string;
  readonly collectionPath: string;
  readonly principalId: string;
  /** Oldest retained collection revision, inclusive. */
  readonly minRevision?: number;
  /** Current collection revision, inclusive. */
  readonly maxRevision?: number;
}

export interface SyncTokenIssueInput {
  readonly collectionId: string;
  readonly collectionPath: string;
  readonly principalId: string;
  readonly revision: number;
}

interface EncodedClaims {
  v: number;
  cid: string;
  cp: string;
  pid: string;
  rev: number;
}

/**
 * Opaque HMAC-authenticated sync tokens. The signature covers the collection
 * identity and path, the authenticated principal and the collection revision.
 * A token issued for another collection, principal or revision range therefore
 * fails with DAV's `valid-sync-token` precondition.
 */
export class SyncTokenCodec {
  readonly #key: Buffer;

  constructor(secret: string | Uint8Array) {
    const key = typeof secret === 'string' ? Buffer.from(secret, 'utf8') : Buffer.from(secret);
    if (key.length === 0) {
      throw new TypeError('sync token secret must not be empty');
    }
    this.#key = key;
  }

  issue(input: SyncTokenIssueInput): string {
    const encoded: EncodedClaims = {
      v: TOKEN_SCHEMA_VERSION,
      cid: input.collectionId,
      cp: input.collectionPath,
      pid: input.principalId,
      rev: input.revision,
    };

    validateEncodedClaims(encoded, true);

    const payload = Buffer.from(JSON.stringify(encoded), 'utf8');
    const mac = this.#sign(payload);
    return `${TOKEN_PREFIX}${payload.toString('base64url')}.${mac.toString('base64url')}`;
  }

  verify(token: string, expected: SyncTokenExpectation): SyncTokenClaims {
    if (typeof token !== 'string' || token.length === 0 || token.length > MAX_TOKEN_LENGTH) {
      throw new InvalidSyncTokenError('malformed');
    }

    if (!token.startsWith(TOKEN_PREFIX)) {
      throw new InvalidSyncTokenError('malformed');
    }

    const pieces = token.slice(TOKEN_PREFIX.length).split('.');
    if (pieces.length !== 2 || !BASE64URL.test(pieces[0]) || !BASE64URL.test(pieces[1])) {
      throw new InvalidSyncTokenError('malformed');
    }

    let payload: Buffer;
    let suppliedMac: Buffer;
    try {
      payload = Buffer.from(pieces[0], 'base64url');
      suppliedMac = Buffer.from(pieces[1], 'base64url');
    } catch {
      throw new InvalidSyncTokenError('malformed');
    }

    if (payload.length === 0 || suppliedMac.length !== 32) {
      throw new InvalidSyncTokenError('malformed');
    }

    const expectedMac = this.#sign(payload);
    if (
      suppliedMac.length !== expectedMac.length ||
      !timingSafeEqual(suppliedMac, expectedMac)
    ) {
      throw new InvalidSyncTokenError('bad-signature');
    }

    let value: unknown;
    try {
      value = JSON.parse(payload.toString('utf8'));
    } catch {
      throw new InvalidSyncTokenError('malformed');
    }

    if (!isEncodedClaims(value)) {
      throw new InvalidSyncTokenError('malformed');
    }

    if (value.cid !== expected.collectionId || value.cp !== expected.collectionPath) {
      throw new InvalidSyncTokenError('collection-mismatch');
    }
    if (value.pid !== expected.principalId) {
      throw new InvalidSyncTokenError('principal-mismatch');
    }

    if (
      (expected.minRevision !== undefined && value.rev < expected.minRevision) ||
      (expected.maxRevision !== undefined && value.rev > expected.maxRevision)
    ) {
      throw new InvalidSyncTokenError('version-out-of-range');
    }

    return {
      tokenVersion: TOKEN_SCHEMA_VERSION,
      collectionId: value.cid,
      collectionPath: value.cp,
      principalId: value.pid,
      revision: value.rev,
    };
  }

  #sign(payload: Buffer): Buffer {
    return createHmac('sha256', this.#key).update(payload).digest();
  }
}

function validateEncodedClaims(claims: EncodedClaims, throwOnError: boolean): boolean {
  const valid =
    claims.v === TOKEN_SCHEMA_VERSION &&
    typeof claims.cid === 'string' &&
    claims.cid.length > 0 &&
    typeof claims.cp === 'string' &&
    claims.cp.startsWith('/') &&
    typeof claims.pid === 'string' &&
    claims.pid.length > 0 &&
    Number.isSafeInteger(claims.rev) &&
    claims.rev >= 0;

  if (!valid && throwOnError) {
    throw new TypeError('invalid sync token claims');
  }
  return valid;
}

function isEncodedClaims(value: unknown): value is EncodedClaims {
  if (typeof value !== 'object' || value === null) {
    return false;
  }

  const candidate = value as Record<string, unknown>;
  return validateEncodedClaims(
    {
      v: candidate.v as number,
      cid: candidate.cid as string,
      cp: candidate.cp as string,
      pid: candidate.pid as string,
      rev: candidate.rev as number,
    },
    false,
  );
}
