import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import type { SubscriptionFilter } from './types.js';

export interface CursorPayload {
  v: number;
  fh: string;
  seq: number;
  off: number;
  ep: number;
}

export type CursorErrorReason = 'malformed' | 'bad-signature' | 'unsupported-version';

export class CursorError extends Error {
  readonly reason: CursorErrorReason;
  constructor(reason: CursorErrorReason, message: string) {
    super(message);
    this.name = 'CursorError';
    this.reason = reason;
  }
}

export class CursorFilterMismatchError extends Error {
  constructor() {
    super('cursor was issued for a different subscription filter and cannot be reused');
    this.name = 'CursorFilterMismatchError';
  }
}

export function normalizeFilter(filter: SubscriptionFilter): { kinds: string[] } {
  const kinds = filter.kinds ? filter.kinds.slice() : [];
  return { kinds: [...new Set(kinds)].sort() };
}

export function filterHash(filter: SubscriptionFilter): string {
  return createHash('sha256').update(JSON.stringify(normalizeFilter(filter))).digest('hex').slice(0, 16);
}

export function matchesFilter(filter: SubscriptionFilter, kind: string): boolean {
  if (!filter.kinds || filter.kinds.length === 0) return true;
  return filter.kinds.includes(kind);
}

export function encodeCursor(payload: CursorPayload, secret: string): string {
  const body = base64urlEncode(JSON.stringify(payload));
  return body + '.' + sign(body, secret);
}

export function decodeCursor(token: string, secret: string): CursorPayload {
  const dot = token.lastIndexOf('.');
  if (dot <= 0 || dot === token.length - 1) {
    throw new CursorError('malformed', 'cursor must have the form <body>.<signature>');
  }
  const body = token.slice(0, dot);
  const sig = token.slice(dot + 1);
  const expected = sign(body, secret);
  if (sig.length !== expected.length || !timingSafeEqual(Buffer.from(sig), Buffer.from(expected))) {
    throw new CursorError('bad-signature', 'cursor signature does not match');
  }
  let parsed: CursorPayload | null;
  try {
    parsed = JSON.parse(base64urlDecode(body));
  } catch {
    throw new CursorError('malformed', 'cursor body is not valid JSON');
  }
  if (parsed === null || typeof parsed !== 'object') {
    throw new CursorError('malformed', 'cursor payload is not an object');
  }
  if (parsed.v !== 1) {
    throw new CursorError('unsupported-version', 'unsupported cursor version: ' + String(parsed.v));
  }
  if (typeof parsed.fh !== 'string' || typeof parsed.seq !== 'number' || typeof parsed.off !== 'number' || typeof parsed.ep !== 'number') {
    throw new CursorError('malformed', 'cursor payload has invalid fields');
  }
  return parsed;
}

function sign(body: string, secret: string): string {
  return urlsafe(createHmac('sha256', secret).update(body).digest('base64'));
}

function base64urlEncode(value: string): string {
  return urlsafe(Buffer.from(value, 'utf8').toString('base64'));
}

function base64urlDecode(value: string): string {
  let b64 = value.split('-').join('+').split('_').join('/');
  while (b64.length % 4 !== 0) b64 += '=';
  return Buffer.from(b64, 'base64').toString('utf8');
}

function urlsafe(b64: string): string {
  return b64.split('+').join('-').split('/').join('_').split('=').join('');
}
