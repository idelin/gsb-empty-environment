import {
  Dependency,
  ResolveResult,
  StoreView,
} from './types';

export const DEFAULT_MAX_HOPS = 32;

export interface ResolveOptions {
  /** Maximum number of alias records dereferenced before giving up. */
  maxHops?: number;
  /** Historical sequence number to resolve against; defaults to latest. */
  snapshot?: number;
}

/**
 * The reference resolver: a plain, allocation-light iterative walk over a
 * StoreView. It performs no caching; every read hits the view and is reported
 * through `onDep` (including reads of missing entries, i.e. negative
 * dependencies). All cached results must agree with this traversal.
 */
export function traverse(
  view: StoreView,
  start: string,
  opts: ResolveOptions = {},
  onDep?: (dep: Dependency) => void,
): ResolveResult {
  const maxHops = opts.maxHops ?? DEFAULT_MAX_HOPS;
  const path: string[] = [start];
  const indexByAlias = new Map<string, number>([[start, 0]]);
  let current = start;

  for (let hop = 0; ; hop++) {
    if (hop >= maxHops) {
      return { ok: false, reason: 'max-hops', path: [...path] };
    }

    const target = view.getAliasTarget(current, opts.snapshot);
    onDep?.({ kind: 'alias', alias: current, existed: target !== null });

    if (target === null) {
      return {
        ok: false,
        reason: 'dangling',
        path: [...path],
        missing: { kind: 'alias', alias: current },
      };
    }

    if (target.kind === 'object') {
      const exists = view.hasObject(target.objectId, target.version, opts.snapshot);
      onDep?.({
        kind: 'object',
        objectId: target.objectId,
        version: target.version,
        existed: exists,
      });
      if (!exists) {
        return {
          ok: false,
          reason: 'dangling',
          path: [...path],
          missing: { kind: 'object', objectId: target.objectId, version: target.version },
        };
      }
      return { ok: true, path: [...path], object: target };
    }

    const next = target.alias;
    const seenAt = indexByAlias.get(next);
    if (seenAt !== undefined) {
      return {
        ok: false,
        reason: 'cycle',
        path: [...path, next],
        cycle: [...path.slice(seenAt), next],
      };
    }
    indexByAlias.set(next, path.length);
    path.push(next);
    current = next;
  }
}

/** Cache-free reference entry point used for cross-checking cached results. */
export function resolveUncached(
  view: StoreView,
  start: string,
  opts: ResolveOptions = {},
): ResolveResult {
  return traverse(view, start, opts);
}
