import { EvalCache, cacheKey } from './cache';
import { evaluateOnSnapshot } from './evaluate';
import { Snapshot, validateConfigs } from './snapshot';
import { EvalResult, FlagConfig, UserContext } from './types';

export interface EvaluateOptions {
  /** Pin evaluation to a retained older snapshot. Defaults to current. */
  version?: number;
  /** Set false to bypass the evaluation cache. Defaults to true. */
  useCache?: boolean;
}

/**
 * Versioned flag config store. Every successful update appends a new
 * immutable snapshot; old snapshots are retained and remain evaluable.
 * A failed update (validation error, dependency cycle) changes nothing.
 */
export class FlagStore {
  private readonly snapshots = new Map<number, Snapshot>();
  private currentVersion = 0;
  private readonly cache: EvalCache;

  constructor(flags: FlagConfig[] = [], cache: EvalCache = new EvalCache()) {
    this.cache = cache;
    if (flags.length > 0) this.update(flags);
  }

  /**
   * Atomically publishes a full config set as a new snapshot.
   * Returns the new version number. Throws (and keeps the store untouched)
   * on duplicate ids, dangling dependencies, or dependency cycles.
   */
  update(flags: FlagConfig[]): number {
    validateConfigs(flags);
    const version = this.currentVersion + 1;
    this.snapshots.set(version, new Snapshot(version, flags));
    this.currentVersion = version;
    return version;
  }

  get version(): number {
    return this.currentVersion;
  }

  getSnapshot(version?: number): Snapshot {
    const v = version ?? this.currentVersion;
    const snapshot = this.snapshots.get(v);
    if (snapshot === undefined) {
      throw new Error(`no snapshot at version ${v}`);
    }
    return snapshot;
  }

  /** All retained snapshot versions, ascending. */
  versions(): number[] {
    return Array.from(this.snapshots.keys()).sort((a, b) => a - b);
  }

  evaluate(flagId: string, user: UserContext, options: EvaluateOptions = {}): EvalResult {
    const snapshot = this.getSnapshot(options.version);
    const useCache = options.useCache !== false;
    let key: string | undefined;
    if (useCache) {
      key = cacheKey({
        snapshotVersion: snapshot.version,
        flagId,
        userId: user.id,
        attrVersion: user.attrVersion ?? 0,
        closureDigest: snapshot.closureDigest(flagId),
      });
      const hit = this.cache.get(key);
      if (hit !== undefined) return hit;
    }
    const result = evaluateOnSnapshot(snapshot, flagId, user);
    if (useCache && key !== undefined) this.cache.set(key, result);
    return result;
  }

  /** Evaluates every flag in the snapshot for one user. */
  evaluateAll(user: UserContext, options: EvaluateOptions = {}): Record<string, EvalResult> {
    const snapshot = this.getSnapshot(options.version);
    const out: Record<string, EvalResult> = {};
    for (const flagId of snapshot.flags.keys()) {
      out[flagId] = this.evaluate(flagId, user, options);
    }
    return out;
  }

  get cacheSize(): number {
    return this.cache.size;
  }
}
