import { buildSnapshot, closureDigest, ConfigError } from "./snapshot";
import { evaluateFlag } from "./evaluator";
import {
  ConfigSnapshot,
  EvaluateOptions,
  Evaluation,
  FlagConfig,
  UserContext,
} from "./types";

/**
 * Cache key: snapshot version + flag + user id + user attributes version +
 * dependency-closure digest. Any change to the flag's transitive dependency
 * subgraph (or to the user's attributes, once the caller bumps the version)
 * produces a different key, so stale entries are simply never hit.
 */
export function cacheKey(
  snapshot: ConfigSnapshot,
  flagId: string,
  user: UserContext
): string {
  return [
    `v${snapshot.version}`,
    flagId,
    user.id,
    `attrs@${user.attributesVersion}`,
    closureDigest(snapshot, flagId),
  ].join("|");
}

export class FlagService {
  private snapshots = new Map<number, ConfigSnapshot>();
  private currentVersion = 0;
  private cache = new Map<string, Evaluation>();

  constructor(flags?: ReadonlyArray<FlagConfig>) {
    if (flags) this.updateConfig(flags);
  }

  /**
   * Validate and publish a new configuration as the next snapshot version.
   * Old snapshots are retained and remain evaluable. If validation fails
   * (e.g. a dependency cycle), the whole update is rejected and the current
   * configuration is left untouched.
   */
  updateConfig(flags: ReadonlyArray<FlagConfig>): ConfigSnapshot {
    const snapshot = buildSnapshot(this.currentVersion + 1, flags, Date.now());
    this.snapshots.set(snapshot.version, snapshot);
    this.currentVersion = snapshot.version;
    return snapshot;
  }

  getSnapshot(version?: number): ConfigSnapshot {
    const v = version ?? this.currentVersion;
    const snapshot = this.snapshots.get(v);
    if (!snapshot) throw new ConfigError(`unknown snapshot version: ${v}`);
    return snapshot;
  }

  listVersions(): number[] {
    return [...this.snapshots.keys()].sort((a, b) => a - b);
  }

  evaluate(flagId: string, user: UserContext, options: EvaluateOptions = {}): Evaluation {
    const snapshot = this.getSnapshot(options.version);
    if (options.useCache === false) {
      return evaluateFlag(snapshot, flagId, user);
    }
    const key = cacheKey(snapshot, flagId, user);
    const hit = this.cache.get(key);
    if (hit) return hit;
    const result = evaluateFlag(snapshot, flagId, user);
    this.cache.set(key, result);
    return result;
  }

  /** Evaluate every flag in the snapshot for one user. */
  evaluateAll(user: UserContext, options: EvaluateOptions = {}): Record<string, Evaluation> {
    const snapshot = this.getSnapshot(options.version);
    const out: Record<string, Evaluation> = {};
    for (const flagId of Object.keys(snapshot.flags)) {
      out[flagId] = this.evaluate(flagId, user, { ...options, version: snapshot.version });
    }
    return out;
  }

  cacheKeyFor(flagId: string, user: UserContext, version?: number): string {
    return cacheKey(this.getSnapshot(version), flagId, user);
  }

  get cacheSize(): number {
    return this.cache.size;
  }

  clearCache(): void {
    this.cache.clear();
  }
}

export function createUser(
  id: string,
  attributes: Record<string, string | number | boolean> = {},
  attributesVersion = 1
): UserContext {
  return { id, attributes, attributesVersion };
}

/** Return a new user context with updated attributes and a bumped version. */
export function withAttributes(
  user: UserContext,
  attributes: Record<string, string | number | boolean>
): UserContext {
  return { ...user, attributes, attributesVersion: user.attributesVersion + 1 };
}
