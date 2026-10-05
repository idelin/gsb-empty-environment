/**
 * Opaque page cursors. A cursor binds:
 *   - the expression hash (canonical, order-insensitive)
 *   - every input set id together with its resolved version
 *   - the page size k
 *   - the last emitted id (resume position)
 * The payload carries a truncated sha256 integrity tag so tampering is detected.
 */
import { createHash } from 'crypto';

export interface CursorPayload {
  v: 1;
  expr: string; // exprHash hex
  inputs: [string, string][]; // [setId, version] sorted by setId
  k: number;
  last: number; // last emitted id, -1 for "before the first id"
}

export class CursorError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CursorError';
  }
}

export class CursorBindingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CursorBindingError';
  }
}

function tag(payload: CursorPayload): string {
  const body = JSON.stringify({
    v: payload.v,
    expr: payload.expr,
    inputs: payload.inputs,
    k: payload.k,
    last: payload.last,
  });
  return createHash('sha256').update('ssidb-cursor:' + body, 'utf8').digest('hex').slice(0, 32);
}

export function encodeCursor(payload: CursorPayload): string {
  const full = { ...payload, h: tag(payload) };
  return Buffer.from(JSON.stringify(full), 'utf8').toString('base64url');
}

export function decodeCursor(cursor: string): CursorPayload {
  let parsed: unknown;
  try {
    parsed = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8'));
  } catch {
    throw new CursorError('cursor is not valid base64url JSON');
  }
  const c = parsed as Record<string, unknown>;
  if (c === null || typeof c !== 'object') throw new CursorError('cursor payload is not an object');
  if (c.v !== 1) throw new CursorError('unsupported cursor version');
  if (typeof c.expr !== 'string' || typeof c.h !== 'string') throw new CursorError('cursor missing fields');
  if (!Array.isArray(c.inputs) || !c.inputs.every((i) => Array.isArray(i) && i.length === 2 && i.every((x) => typeof x === 'string'))) {
    throw new CursorError('cursor inputs malformed');
  }
  if (!Number.isInteger(c.k) || (c.k as number) < 1) throw new CursorError('cursor page size malformed');
  if (!Number.isInteger(c.last) || (c.last as number) < -1 || (c.last as number) > 0xffffffff) {
    throw new CursorError('cursor position malformed');
  }
  const payload: CursorPayload = {
    v: 1,
    expr: c.expr,
    inputs: c.inputs as [string, string][],
    k: c.k as number,
    last: c.last as number,
  };
  if (tag(payload) !== c.h) throw new CursorError('cursor integrity check failed (tampered or corrupt)');
  return payload;
}

/** Verify a decoded cursor against the current request's expression hash and resolved inputs. */
export function assertCursorBinding(
  payload: CursorPayload,
  exprHash: string,
  inputs: [string, string][],
): void {
  if (payload.expr !== exprHash) {
    throw new CursorBindingError('cursor was issued for a different expression');
  }
  if (payload.inputs.length !== inputs.length) {
    throw new CursorBindingError('cursor was issued for a different input set list');
  }
  for (let i = 0; i < inputs.length; i++) {
    const [wantSet, wantVer] = payload.inputs[i];
    const [gotSet, gotVer] = inputs[i];
    if (wantSet !== gotSet || wantVer !== gotVer) {
      throw new CursorBindingError(
        'cursor input mismatch for set "' + wantSet + '": cursor bound to version "' + wantVer + '" but current version is "' + gotVer + '"',
      );
    }
  }
}
