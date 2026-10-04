import { createHash } from 'node:crypto';
import { normalizeFilter, type Filter } from './types.ts';

export const CURSOR_VERSION = 1;

/** 游标内容：绑定订阅条件哈希 + 已消费到的序列号。 */
export interface CursorPayload {
  v: number;
  filterHash: string;
  seq: number;
}

export type CursorErrorCode =
  | 'CURSOR_MALFORMED'
  | 'CURSOR_UNSUPPORTED_VERSION'
  | 'CURSOR_FILTER_MISMATCH'
  | 'CURSOR_TOO_OLD';

export class CursorError extends Error {
  readonly code: CursorErrorCode;
  constructor(code: CursorErrorCode, message: string) {
    super(message);
    this.name = 'CursorError';
    this.code = code;
  }
}

/** 订阅条件的稳定哈希：游标与订阅条件绑定，防止跨条件复用。 */
export function filterHash(filter: Filter): string {
  const normalized = normalizeFilter(filter);
  return createHash('sha256')
    .update(JSON.stringify(normalized))
    .digest('base64url')
    .slice(0, 16);
}

export function encodeCursor(filter: Filter, seq: number): string {
  const payload: CursorPayload = {
    v: CURSOR_VERSION,
    filterHash: filterHash(filter),
    seq,
  };
  return Buffer.from(JSON.stringify(payload), 'utf8').toString('base64url');
}

export function decodeCursor(token: string): CursorPayload {
  let raw: string;
  try {
    raw = Buffer.from(token, 'base64url').toString('utf8');
  } catch {
    throw new CursorError('CURSOR_MALFORMED', 'cursor is not valid base64url');
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new CursorError('CURSOR_MALFORMED', 'cursor payload is not JSON');
  }
  const payload = parsed as Partial<CursorPayload>;
  if (
    typeof payload !== 'object' ||
    payload === null ||
    typeof payload.v !== 'number' ||
    typeof payload.filterHash !== 'string' ||
    typeof payload.seq !== 'number' ||
    !Number.isInteger(payload.seq) ||
    payload.seq < 0
  ) {
    throw new CursorError('CURSOR_MALFORMED', 'cursor payload has invalid shape');
  }
  if (payload.v !== CURSOR_VERSION) {
    throw new CursorError(
      'CURSOR_UNSUPPORTED_VERSION',
      `cursor version ${payload.v} is not supported`,
    );
  }
  return payload as CursorPayload;
}

/**
 * 校验游标是否可用于本次订阅。
 * 通过校验后返回游标中的序列号；不可续传时抛出 CursorError。
 * CURSOR_TOO_OLD 由调用方捕获后走快照恢复路径，其余错误直接失败。
 */
export function validateCursorForFilter(
  token: string,
  filter: Filter,
  compactedBefore: number,
): number {
  const payload = decodeCursor(token);
  if (payload.filterHash !== filterHash(filter)) {
    throw new CursorError(
      'CURSOR_FILTER_MISMATCH',
      'cursor was issued for a different subscription filter and cannot be reused',
    );
  }
  if (payload.seq < compactedBefore) {
    throw new CursorError(
      'CURSOR_TOO_OLD',
      `cursor seq ${payload.seq} is older than compaction watermark ${compactedBefore}`,
    );
  }
  return payload.seq;
}
