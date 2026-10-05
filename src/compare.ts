import type { TraceEvent, ValueDiff } from './types.js';

export function eventView(e: TraceEvent): Record<string, unknown> {
  return {
    args: e.args === undefined ? null : e.args,
    result: e.result === undefined ? null : e.result,
    status: e.status === undefined ? 'ok' : e.status,
    error: e.error === undefined ? null : e.error,
  };
}

function jsonable(v: unknown): unknown {
  return v === undefined ? null : v;
}

function record(out: ValueDiff[], limit: number, path: string, left: unknown, right: unknown): void {
  if (out.length >= limit) return;
  out.push({ path: path, left: jsonable(left), right: jsonable(right) });
}

function isIdent(key: string): boolean {
  if (key.length === 0) return false;
  for (let i = 0; i < key.length; i++) {
    const c = key.charCodeAt(i);
    const isAlpha = (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c === 95 || c === 36;
    const isDigit = c >= 48 && c <= 57;
    if (!isAlpha && !(isDigit && i > 0)) return false;
  }
  return true;
}

function childPath(path: string, key: string): string {
  return isIdent(key) ? path + '.' + key : path + '[' + JSON.stringify(key) + ']';
}

function walk(
  left: any,
  right: any,
  ignore: Set<string>,
  tolerance: number,
  path: string,
  out: ValueDiff[],
  limit: number
): void {
  if (out.length >= limit) return;
  if (typeof left === 'number' && typeof right === 'number') {
    if (left === right) return;
    if (Number.isNaN(left) && Number.isNaN(right)) return;
    if (Math.abs(left - right) <= tolerance) return;
    record(out, limit, path, left, right);
    return;
  }
  if (left === null || right === null || typeof left !== 'object' || typeof right !== 'object') {
    if (left !== right) record(out, limit, path, left, right);
    return;
  }
  const leftArr = Array.isArray(left);
  const rightArr = Array.isArray(right);
  if (leftArr !== rightArr) {
    record(out, limit, path, left, right);
    return;
  }
  if (leftArr) {
    const common = Math.min(left.length, right.length);
    for (let i = 0; i < common; i++) {
      walk(left[i], right[i], ignore, tolerance, path + '[' + i + ']', out, limit);
    }
    if (left.length !== right.length) record(out, limit, path + '.length', left.length, right.length);
    return;
  }
  const keys: Set<string> = new Set();
  for (const k of Object.keys(left)) {
    if (!ignore.has(k)) keys.add(k);
  }
  for (const k of Object.keys(right)) {
    if (!ignore.has(k)) keys.add(k);
  }
  for (const k of keys) {
    if (out.length >= limit) return;
    const hasL = Object.prototype.hasOwnProperty.call(left, k);
    const hasR = Object.prototype.hasOwnProperty.call(right, k);
    const child = childPath(path, k);
    if (hasL && hasR) {
      walk(left[k], right[k], ignore, tolerance, child, out, limit);
    } else if (hasL) {
      record(out, limit, child, left[k], undefined);
    } else {
      record(out, limit, child, undefined, right[k]);
    }
  }
}

export function deepDiff(
  left: unknown,
  right: unknown,
  ignore: Set<string>,
  tolerance: number,
  path: string,
  out: ValueDiff[],
  limit: number
): ValueDiff[] {
  walk(left, right, ignore, tolerance, path, out, limit);
  return out;
}
