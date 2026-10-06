import { ConfigSnapshot, FlagConfig, GroupRule, Rollout } from "./types";
import { hashString } from "./hash";
import { hasOwn } from "./conditions";

export class ConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConfigError";
  }
}

/** Higher priority first; ties broken by ascending rule id. */
export function compareGroups(a: GroupRule, b: GroupRule): number {
  if (a.priority !== b.priority) return b.priority - a.priority;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

function validateRollout(rollout: Rollout | undefined, owner: string): void {
  if (!rollout) return;
  if (
    !Number.isInteger(rollout.basisPoints) ||
    rollout.basisPoints < 0 ||
    rollout.basisPoints > 10000
  ) {
    throw new ConfigError(
      `${owner}: rollout.basisPoints must be an integer in [0, 10000], got ${rollout.basisPoints}`
    );
  }
}

function normalizeFlag(flag: FlagConfig): FlagConfig {
  validateRollout(flag.rollout, `flag "${flag.id}"`);
  const groups = [...(flag.groups ?? [])].sort(compareGroups);
  for (const group of groups) {
    validateRollout(group.rollout, `flag "${flag.id}" group "${group.id}"`);
  }
  return {
    ...flag,
    overrides: flag.overrides ?? {},
    groups,
    dependsOn: flag.dependsOn ?? [],
  };
}

/**
 * Dependency targets must exist in the same snapshot, and the dependency
 * graph must be acyclic. Any violation throws, so the caller can reject the
 * whole update atomically.
 */
function validateDependencies(flags: Record<string, FlagConfig>): void {
  for (const id of Object.keys(flags)) {
    for (const dep of flags[id].dependsOn ?? []) {
      if (!hasOwn(flags, dep.flagId)) {
        throw new ConfigError(
          `flag "${id}" depends on unknown flag "${dep.flagId}"`
        );
      }
    }
  }

  const WHITE = 0;
  const GRAY = 1;
  const BLACK = 2;
  const color: Record<string, number> = {};
  const stack: string[] = [];

  const visit = (id: string): void => {
    color[id] = GRAY;
    stack.push(id);
    for (const dep of flags[id].dependsOn ?? []) {
      if (color[dep.flagId] === GRAY) {
        const cycle = [...stack.slice(stack.indexOf(dep.flagId)), dep.flagId];
        throw new ConfigError(`dependency cycle detected: ${cycle.join(" -> ")}`);
      }
      if (color[dep.flagId] !== BLACK) visit(dep.flagId);
    }
    stack.pop();
    color[id] = BLACK;
  };

  for (const id of Object.keys(flags)) {
    if (!color[id]) visit(id);
  }
}

function deepFreeze<T>(value: T): T {
  if (value && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

/**
 * Build an immutable snapshot. Throws ConfigError on any validation failure,
 * in which case no snapshot is produced and the caller keeps its old state.
 */
export function buildSnapshot(
  version: number,
  flags: ReadonlyArray<FlagConfig>,
  createdAtMs: number
): ConfigSnapshot {
  const byId: Record<string, FlagConfig> = {};
  for (const flag of flags) {
    if (hasOwn(byId, flag.id)) {
      throw new ConfigError(`duplicate flag id: "${flag.id}"`);
    }
    byId[flag.id] = normalizeFlag(flag);
  }
  validateDependencies(byId);
  return { version, flags: deepFreeze(byId), createdAtMs };
}

/** Sorted transitive dependency closure of `flagId`, including itself. */
export function dependencyClosure(
  snapshot: ConfigSnapshot,
  flagId: string
): string[] {
  const seen = new Set<string>();
  const visit = (id: string): void => {
    if (seen.has(id)) return;
    seen.add(id);
    const flag = snapshot.flags[id];
    for (const dep of flag?.dependsOn ?? []) visit(dep.flagId);
  };
  visit(flagId);
  return [...seen].sort();
}

/**
 * Digest of everything that can influence the evaluation of `flagId`: the
 * canonical serialization of every flag in its dependency closure.
 */
export function closureDigest(snapshot: ConfigSnapshot, flagId: string): string {
  const canonical = dependencyClosure(snapshot, flagId)
    .map((id) => {
      const f = snapshot.flags[id];
      return JSON.stringify([
        id,
        f.defaultValue,
        f.fallbackValue ?? false,
        f.overrides ?? {},
        f.groups ?? [],
        f.rollout ?? null,
        (f.dependsOn ?? []).map((d) => [d.flagId, d.equals]),
      ]);
    })
    .join("\n");
  return hashString(canonical).toString(16).padStart(8, "0");
}
