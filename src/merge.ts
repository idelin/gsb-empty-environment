import { JsonDagError, deepEqual, isObject } from './json.ts';
import type { Json, Path } from './json.ts';
import { elementKey } from './digest.ts';
import type { DigestOptions } from './digest.ts';
import { diff } from './diff.ts';
import type { SourceInfo } from './diff.ts';
import { patchFromChanges } from './patch.ts';
import type { Patch } from './patch.ts';

export interface Conflict {
  path: Path;
  /** undefined means the value was deleted on that side. */
  base?: Json;
  left?: Json;
  right?: Json;
  reason: 'both-added-differ' | 'delete-vs-modify' | 'both-modified' | 'order-conflict';
}

export interface MergeResult {
  /** Best-effort merge; conflict paths conservatively keep the base value. */
  merged: Json;
  conflicts: Conflict[];
  /** Atomic patch from base to merged. */
  patch: Patch;
  /** Provenance of merged nodes in the base tree (copies are never moves). */
  sourceMap: Map<string, SourceInfo>;
}

export function merge3(base: Json, left: Json, right: Json, opts: DigestOptions = {}): MergeResult {
  const conflicts: Conflict[] = [];
  const idKey = opts.idKey ?? 'id';
  const modeAt = (path: Path) => opts.arrayMode?.(path) ?? 'position';

  const conflict = (
    path: Path,
    b: Json | undefined,
    l: Json | undefined,
    r: Json | undefined,
    reason: Conflict['reason'],
  ): Json | undefined => {
    conflicts.push({ path, base: b, left: l, right: r, reason });
    return b; // conservative resolution: keep the base value
  };

  function mergeValue(b: Json | undefined, l: Json | undefined, r: Json | undefined, path: Path): Json | undefined {
    if (deepEqual(l, r)) return l;
    if (b !== undefined && deepEqual(b, l)) return r;
    if (b !== undefined && deepEqual(b, r)) return l;
    if (b === undefined) {
      if (isObject(l) && isObject(r)) return mergeObject(undefined, l, r, path);
      if (Array.isArray(l) && Array.isArray(r) && modeAt(path) === 'id') return mergeArray(undefined, l, r, path);
      return conflict(path, undefined, l, r, 'both-added-differ');
    }
    if (isObject(b) && isObject(l) && isObject(r)) return mergeObject(b, l, r, path);
    if (Array.isArray(b) && Array.isArray(l) && Array.isArray(r) && modeAt(path) === 'id') {
      return mergeArray(b, l, r, path);
    }
    const reason = l === undefined || r === undefined ? 'delete-vs-modify' : 'both-modified';
    return conflict(path, b, l, r, reason);
  }

  function mergeObject(
    b: Record<string, Json> | undefined,
    l: Record<string, Json>,
    r: Record<string, Json>,
    path: Path,
  ): Json {
    const out: Record<string, Json> = {};
    const keys = new Set([...Object.keys(b ?? {}), ...Object.keys(l), ...Object.keys(r)]);
    for (const k of keys) {
      const v = mergeValue(b?.[k], l[k], r[k], [...path, k]);
      if (v !== undefined) out[k] = v;
    }
    return out;
  }

  function mergeArray(b: Json[] | undefined, l: Json[], r: Json[], path: Path): Json[] {
    const keysOf = (arr: Json[] | undefined): string[] => {
      const keys: string[] = [];
      const seen = new Set<string>();
      (arr ?? []).forEach((el, i) => {
        const k = elementKey(el, i, 'id', idKey, path);
        if (seen.has(k)) {
          throw new JsonDagError(`duplicate array element id ${JSON.stringify(k.slice(3))}`, [...path, i]);
        }
        seen.add(k);
        keys.push(k);
      });
      return keys;
    };
    const bArr = b ?? [];
    const bKeys = keysOf(b);
    const lKeys = keysOf(l);
    const rKeys = keysOf(r);
    const bMap = new Map(bKeys.map((k, i) => [k, bArr[i]] as const));
    const lMap = new Map(lKeys.map((k, i) => [k, l[i]] as const));
    const rMap = new Map(rKeys.map((k, i) => [k, r[i]] as const));

    // Survivors: present after applying both sides' deletions; delete-vs-modify
    // keeps the element (conflict reported later, base value retained).
    const keep = new Set<string>();
    for (const k of new Set([...bKeys, ...lKeys, ...rKeys])) {
      const inB = bMap.has(k);
      const inL = lMap.has(k);
      const inR = rMap.has(k);
      if (inL && inR) keep.add(k);
      else if (inL) {
        if (!inB || !deepEqual(bMap.get(k), lMap.get(k))) keep.add(k);
      } else if (inR) {
        if (!inB || !deepEqual(bMap.get(k), rMap.get(k))) keep.add(k);
      }
    }

    const filt = (keys: string[]) => keys.filter((k) => keep.has(k));
    const bOrd = filt(bKeys);
    const lOrd = filt(lKeys);
    const rOrd = filt(rKeys);
    const sameSeq = (a: string[], c: string[]) => a.length === c.length && a.every((k, i) => k === c[i]);
    const lReordered = !sameSeq(
      lOrd.filter((k) => bMap.has(k)),
      bOrd.filter((k) => lMap.has(k)),
    );
    const rReordered = !sameSeq(
      rOrd.filter((k) => bMap.has(k)),
      bOrd.filter((k) => rMap.has(k)),
    );
    let order: string[];
    if (lReordered && rReordered) {
      conflict(path, bArr, l, r, 'order-conflict');
      order = lOrd;
    } else if (lReordered) order = lOrd;
    else if (rReordered) order = rOrd;
    else order = bOrd;
    for (const k of [...lOrd, ...rOrd]) if (!order.includes(k)) order.push(k);

    const out: Json[] = [];
    order.forEach((k, i) => {
      const v = mergeValue(bMap.get(k), lMap.get(k), rMap.get(k), [...path, i]);
      if (v !== undefined) out.push(v);
    });
    return out;
  }

  const merged = mergeValue(base, left, right, []) ?? null;
  const d = diff(base, merged, opts);
  return { merged, conflicts, patch: patchFromChanges(base, merged, d.changes, opts), sourceMap: d.sourceMap };
}
