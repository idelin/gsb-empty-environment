import { bucketKey, bucketOf } from './hash';
import {
  AttributeCondition,
  EvalResult,
  UserContext,
} from './types';
import { Snapshot } from './snapshot';

export class UnknownFlagError extends Error {
  constructor(flagId: string) {
    super(`unknown flag: ${flagId}`);
    this.name = 'UnknownFlagError';
  }
}

/**
 * A missing attribute never equals anything — not even the empty string.
 * An explicitly present empty string is a real value and matches `eq ''`.
 */
export function matchesCondition(
  cond: AttributeCondition,
  attributes: Record<string, string> | undefined,
): boolean {
  const actual = attributes === undefined ? undefined : attributes[cond.key];
  if (actual === undefined) return false;
  switch (cond.op) {
    case 'eq':
      return actual === cond.value;
    case 'neq':
      return actual !== cond.value;
    case 'in':
      return Array.isArray(cond.value) && cond.value.indexOf(actual) !== -1;
  }
}

function rolloutCheck(
  flagId: string,
  userId: string,
  rollout: { percentage: number; salt?: string } | undefined,
): { pass: boolean; bucket: number; key: string } {
  const percentage = rollout === undefined ? 100 : rollout.percentage;
  const salt = rollout === undefined ? undefined : rollout.salt;
  const key = bucketKey(flagId, userId, salt);
  const bucket = bucketOf(key);
  return { pass: bucket < percentage * 100, bucket, key };
}

/**
 * Evaluates one flag against one immutable snapshot. Dependencies are
 * resolved recursively against the SAME snapshot, so a result never mixes
 * config versions. The store rejects dependency cycles at update time, so
 * `visiting` is only a defensive guard here.
 *
 * The result explains itself: reason, matched group, and the bucket/bucket
 * key of the last rollout check (populated even when the rollout failed).
 */
export function evaluateOnSnapshot(
  snapshot: Snapshot,
  flagId: string,
  user: UserContext,
  visiting: Set<string> = new Set(),
): EvalResult {
  const config = snapshot.flags.get(flagId);
  if (config === undefined) throw new UnknownFlagError(flagId);
  if (visiting.has(flagId)) {
    throw new Error(`dependency cycle reached at evaluation: ${flagId}`);
  }
  visiting.add(flagId);
  try {
    const dependencies = (config.dependencies ?? []).map((dep) => {
      const depResult = evaluateOnSnapshot(snapshot, dep.flagId, user, visiting);
      return {
        flagId: dep.flagId,
        requiredValue: dep.requiredValue,
        actualValue: depResult.value,
        met: depResult.value === dep.requiredValue,
      };
    });

    if (dependencies.some((d) => !d.met)) {
      return {
        flagId,
        value: config.fallbackValue !== undefined ? config.fallbackValue : config.defaultValue,
        reason: 'dependency_unmet',
        dependencies,
        snapshotVersion: snapshot.version,
      };
    }

    if (config.overrides !== undefined && Object.prototype.hasOwnProperty.call(config.overrides, user.id)) {
      return {
        flagId,
        value: config.overrides[user.id],
        reason: 'override',
        dependencies,
        snapshotVersion: snapshot.version,
      };
    }

    const groups = (config.groups ?? [])
      .slice()
      .sort((a, b) => b.priority - a.priority || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));

    let lastBucket: number | undefined;
    let lastBucketKey: string | undefined;
    for (const group of groups) {
      if (!group.conditions.every((c) => matchesCondition(c, user.attributes))) continue;
      const { pass, bucket, key } = rolloutCheck(flagId, user.id, group.rollout);
      lastBucket = bucket;
      lastBucketKey = key;
      if (pass) {
        return {
          flagId,
          value: group.value,
          reason: 'group',
          matchedGroupId: group.id,
          bucket,
          bucketKey: key,
          dependencies,
          snapshotVersion: snapshot.version,
        };
      }
    }

    return {
      flagId,
      value: config.defaultValue,
      reason: 'default',
      bucket: lastBucket,
      bucketKey: lastBucketKey,
      dependencies,
      snapshotVersion: snapshot.version,
    };
  } finally {
    visiting.delete(flagId);
  }
}
