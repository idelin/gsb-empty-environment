// 规范化序列化与哈希：保证同一逻辑内容得到唯一摘要。
import { createHash } from 'node:crypto';
import type { JsonValue, LogRecord, Checkpoint } from './types.ts';

export const GENESIS = 'GENESIS';

// 递归排序对象键，输出规范化 JSON（与键序无关）。
// 与 JSON 语义对齐：对象中值为 undefined 的键被忽略，数组中的 undefined 视为 null。
export function canonical(value: JsonValue): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value);
  if (Array.isArray(value)) {
    return '[' + value.map((v) => (v === undefined ? 'null' : canonical(v))).join(',') + ']';
  }
  const keys = Object.keys(value)
    .filter((k) => (value as Record<string, JsonValue>)[k] !== undefined)
    .sort();
  const parts = keys.map((k) => JSON.stringify(k) + ':' + canonical((value as Record<string, JsonValue>)[k]));
  return '{' + parts.join(',') + '}';
}

export function sha256(text: string): string {
  return createHash('sha256').update(text, 'utf8').digest('hex');
}

export function hashRecord(seq: number, prev: string, body: unknown): string {
  return sha256(canonical({ seq, prev, body } as unknown as JsonValue));
}

export function recordHashOf(r: LogRecord): string {
  return hashRecord(r.seq, r.prev, r.body);
}

export function checkpointHash(c: Omit<Checkpoint, 'hash'>): string {
  return sha256(
    canonical({
      id: c.id,
      uptoSeq: c.uptoSeq,
      prefixHash: c.prefixHash,
      state: c.state,
      appliedOpIds: c.appliedOpIds,
      confirmedEffects: c.confirmedEffects,
      pendingEffects: c.pendingEffects,
    } as unknown as JsonValue),
  );
}

// 状态摘要：恢复完成后用于与原始状态比对。
export function stateSummary(state: Record<string, JsonValue>): string {
  return sha256(canonical(state as JsonValue));
}

export function deepClone<T>(v: T): T {
  return v === undefined ? v : (JSON.parse(JSON.stringify(v)) as T);
}

export function deepEqual(a: unknown, b: unknown): boolean {
  return canonical(a as JsonValue) === canonical(b as JsonValue);
}
