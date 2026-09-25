import type { Json } from '../src/json.ts';
import type { Op } from '../src/patch.ts';

export function deepFreeze<T>(v: T): T {
  if (v !== null && typeof v === 'object') {
    for (const k of Object.keys(v)) deepFreeze((v as Record<string, unknown>)[k]);
    Object.freeze(v);
  }
  return v;
}

export function frozen<T extends Json>(v: T): T {
  return deepFreeze(JSON.parse(JSON.stringify(v)) as T);
}

export function frozenOps(ops: Op[]): Op[] {
  return deepFreeze(JSON.parse(JSON.stringify(ops)) as Op[]);
}
