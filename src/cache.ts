import { DEFAULT_MAX_HOPS, traverse } from './resolver';
import { Store } from './store';
import {
  Dependency,
  dependencyKey,
  ResolveResult,
  StoreChange,
} from './types';

export interface CacheEntry {
  readonly result: ResolveResult;
  /** Positive and negative dependencies observed while resolving. */
  readonly deps: readonly Dependency[];
  /** Store sequence at which the entry was computed. */
  readonly seq: number;
}

export interface CachedResolverOptions {
  maxHops?: number;
}

/**
 * Memoizing resolver over the latest store state.
 *
 * Every cached entry records the exact reads the traversal performed,
 * including negative dependencies (aliases and object versions that were
 * absent). A committed change invalidates precisely the entries whose
 * dependency keys intersect the change set, so filling in a previously
 * missing target flushes the entries that had resolved to "dangling".
 *
 * Historical snapshots are immutable by construction, so callers resolving
 * at an old snapshot should use `resolveUncached` directly; this cache only
 * serves the moving "latest" view.
 */
export class CachedResolver {
  private readonly entries = new Map<string, CacheEntry>();
  private readonly maxHops: number;

  constructor(
    private readonly store: Store,
    opts: CachedResolverOptions = {},
  ) {
    this.maxHops = opts.maxHops ?? DEFAULT_MAX_HOPS;
    store.onChange((change) => this.invalidate(change));
  }

  resolve(name: string): ResolveResult {
    const hit = this.entries.get(name);
    if (hit) return hit.result;
    const deps: Dependency[] = [];
    const result = traverse(this.store, name, { maxHops: this.maxHops }, (dep) => {
      deps.push(dep);
    });
    this.entries.set(name, { result, deps, seq: this.store.currentSeq });
    return result;
  }

  /** Cache contents for a name (test/diagnostic introspection). */
  inspect(name: string): CacheEntry | undefined {
    return this.entries.get(name);
  }

  get size(): number {
    return this.entries.size;
  }

  clear(): void {
    this.entries.clear();
  }

  private invalidate(change: StoreChange): void {
    const changedKeys = new Set<string>();
    for (const alias of change.aliases) changedKeys.add(`alias:${alias}`);
    for (const obj of change.objects) changedKeys.add(`object:${obj.objectId}@${obj.version}`);
    if (changedKeys.size === 0) return;
    for (const [name, entry] of this.entries) {
      if (entry.deps.some((dep) => changedKeys.has(dependencyKey(dep)))) {
        this.entries.delete(name);
      }
    }
  }
}
