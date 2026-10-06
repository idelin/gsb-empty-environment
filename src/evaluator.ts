import { bucketKey, bucketOf } from "./hash";
import { hasOwn, matchesAll } from "./conditions";
import { ConfigSnapshot, Evaluation, FlagConfig, Rollout, UserContext } from "./types";

export class FlagNotFoundError extends Error {
  constructor(flagId: string) {
    super(`unknown flag: "${flagId}"`);
    this.name = "FlagNotFoundError";
  }
}

function rolloutBucket(
  rollout: Rollout,
  scope: ReadonlyArray<string>,
  userId: string
): number {
  return bucketOf(bucketKey([rollout.salt ?? "", ...scope, userId]));
}

function fallbackOf(flag: FlagConfig) {
  return flag.fallbackValue ?? false;
}

/**
 * Pure evaluation of one flag for one user against one snapshot.
 * Dependencies are resolved recursively inside the SAME snapshot.
 */
export function evaluateFlag(
  snapshot: ConfigSnapshot,
  flagId: string,
  user: UserContext
): Evaluation {
  return evaluateInternal(snapshot, flagId, user, []);
}

function evaluateInternal(
  snapshot: ConfigSnapshot,
  flagId: string,
  user: UserContext,
  stack: string[]
): Evaluation {
  const flag = snapshot.flags[flagId];
  if (!flag) throw new FlagNotFoundError(flagId);
  if (stack.includes(flagId)) {
    // Unreachable for snapshots built via buildSnapshot (cycles are rejected
    // at update time); kept as a defensive guard.
    throw new Error(`dependency cycle at evaluation: ${[...stack, flagId].join(" -> ")}`);
  }
  const base = { flagId, snapshotVersion: snapshot.version };

  // 1. Dependencies first: every condition is evaluated in this same snapshot.
  for (const dep of flag.dependsOn ?? []) {
    const depResult = evaluateInternal(snapshot, dep.flagId, user, [...stack, flagId]);
    if (depResult.value !== dep.equals) {
      return { ...base, value: fallbackOf(flag), reason: "dependency-unmet" };
    }
  }

  // 2. Explicit user override beats everything else.
  if (flag.overrides && hasOwn(flag.overrides, user.id)) {
    return { ...base, value: flag.overrides[user.id], reason: "override" };
  }

  // 3. Group rules, already sorted by (priority desc, id asc).
  for (const group of flag.groups ?? []) {
    if (!matchesAll(group.conditions, user.attributes)) continue;
    if (group.rollout) {
      const bucket = rolloutBucket(group.rollout, [flagId, group.id], user.id);
      if (bucket >= group.rollout.basisPoints) continue;
      return { ...base, value: group.value, reason: "group", groupId: group.id, bucket };
    }
    return { ...base, value: group.value, reason: "group", groupId: group.id };
  }

  // 4. Rollout gate around the default value.
  if (flag.rollout) {
    const bucket = rolloutBucket(flag.rollout, [flagId], user.id);
    if (bucket < flag.rollout.basisPoints) {
      return { ...base, value: flag.defaultValue, reason: "default", bucket };
    }
    return { ...base, value: fallbackOf(flag), reason: "fallback", bucket };
  }

  return { ...base, value: flag.defaultValue, reason: "default" };
}
