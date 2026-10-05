import type { AliasStore } from './store.ts';
import type { Hop, Resolution, Snapshot } from './types.ts';
import { aliasKey, objectKey } from './types.ts';

export const DEFAULT_MAX_HOPS = 32;

interface CacheEntry {
  epoch: number;
  result: Resolution;
  deps: ReadonlySet<string>;
}

export interface ResolveOptions {
  maxHops?: number;
  snapshot?: Snapshot;
}

/**
 * Resolves aliases against a store, caching results with fine-grained
 * dependency tracking. Dependencies include negative entries: a lookup that
 * missed still records its key, so creating the previously missing alias or
 * object version later invalidates the cached "dangling"/"missing" result.
 */
export class AliasResolver {
  private cache = new Map<string, CacheEntry>();
  readonly stats = { hits: 0, misses: 0 };
  private readonly store: AliasStore;

  constructor(store: AliasStore) {
    this.store = store;
  }

  resolve(name: string, options: ResolveOptions = {}): Resolution {
    const maxHops = options.maxHops ?? DEFAULT_MAX_HOPS;
    const snap = options.snapshot ?? this.store.snapshot();
    const key = `${maxHops} ${name}`;
    const cached = this.cache.get(key);
    if (cached !== undefined && this.isValid(cached, snap.epoch)) {
      this.stats.hits += 1;
      return cached.result;
    }
    this.stats.misses += 1;
    const deps = new Set<string>();
    const result = traverse(snap, name, maxHops, deps);
    this.cache.set(key, { epoch: snap.epoch, result, deps });
    return result;
  }

  /**
   * A cached entry is valid for a target epoch iff it was computed no later
   * than that epoch and no commit in between touched any of its dependencies
   * (positive or negative).
   */
  private isValid(entry: CacheEntry, epoch: number): boolean {
    if (entry.epoch > epoch) return false;
    for (const record of this.store.commitsBetween(entry.epoch, epoch)) {
      for (const key of record.changed) {
        if (entry.deps.has(key)) return false;
      }
    }
    return true;
  }

  invalidateAll(): void {
    this.cache.clear();
  }
}

/**
 * Cache-free traversal core. Records every consulted dependency key into
 * `deps`, including keys for lookups that missed (negative dependencies).
 */
export function traverse(
  snap: Snapshot,
  start: string,
  maxHops: number,
  deps: Set<string>,
): Resolution {
  const path: Hop[] = [];
  const indexByAlias = new Map<string, number>();
  let current = start;
  for (;;) {
    deps.add(aliasKey(current));
    const target = snap.aliases.get(current);
    if (target === undefined) {
      if (path.length === 0) return { status: 'missing-alias', name: current };
      return { status: 'dangling', path, missing: { kind: 'alias', name: current } };
    }
    const seenAt = indexByAlias.get(current);
    if (seenAt !== undefined) {
      const cycle = path.slice(seenAt).map((hop) => hop.alias);
      cycle.push(current);
      return { status: 'cycle', path, cycle };
    }
    indexByAlias.set(current, path.length);
    path.push({ alias: current, target });
    if (target.kind === 'object') {
      deps.add(objectKey(target.name, target.version));
      const versions = snap.objects.get(target.name);
      if (versions === undefined || !versions.has(target.version)) {
        return { status: 'dangling', path, missing: target };
      }
      return { status: 'ok', path, object: { name: target.name, version: target.version } };
    }
    if (path.length >= maxHops) {
      return { status: 'max-hops-exceeded', path };
    }
    current = target.name;
  }
}
