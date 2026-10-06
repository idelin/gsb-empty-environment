"use strict";
Object.defineProperty(exports, "__esModule", { value: true });

const { bucketKey, bucketOf } = require("./hash");
const { hasOwn, matchesAll } = require("./conditions");

class FlagNotFoundError extends Error {
  constructor(flagId) {
    super(`unknown flag: "${flagId}"`);
    this.name = "FlagNotFoundError";
  }
}

function rolloutBucket(rollout, scope, userId) {
  return bucketOf(bucketKey([rollout.salt ?? "", ...scope, userId]));
}

function fallbackOf(flag) {
  return flag.fallbackValue ?? false;
}

function evaluateFlag(snapshot, flagId, user) {
  return evaluateInternal(snapshot, flagId, user, []);
}

function evaluateInternal(snapshot, flagId, user, stack) {
  const flag = snapshot.flags[flagId];
  if (!flag) throw new FlagNotFoundError(flagId);
  if (stack.includes(flagId)) {
    throw new Error(`dependency cycle at evaluation: ${[...stack, flagId].join(" -> ")}`);
  }
  const base = { flagId, snapshotVersion: snapshot.version };

  for (const dep of flag.dependsOn ?? []) {
    const depResult = evaluateInternal(snapshot, dep.flagId, user, [...stack, flagId]);
    if (depResult.value !== dep.equals) {
      return { ...base, value: fallbackOf(flag), reason: "dependency-unmet" };
    }
  }

  if (flag.overrides && hasOwn(flag.overrides, user.id)) {
    return { ...base, value: flag.overrides[user.id], reason: "override" };
  }

  for (const group of flag.groups ?? []) {
    if (!matchesAll(group.conditions, user.attributes)) continue;
    if (group.rollout) {
      const bucket = rolloutBucket(group.rollout, [flagId, group.id], user.id);
      if (bucket >= group.rollout.basisPoints) continue;
      return { ...base, value: group.value, reason: "group", groupId: group.id, bucket };
    }
    return { ...base, value: group.value, reason: "group", groupId: group.id };
  }

  if (flag.rollout) {
    const bucket = rolloutBucket(flag.rollout, [flagId], user.id);
    if (bucket < flag.rollout.basisPoints) {
      return { ...base, value: flag.defaultValue, reason: "default", bucket };
    }
    return { ...base, value: fallbackOf(flag), reason: "fallback", bucket };
  }

  return { ...base, value: flag.defaultValue, reason: "default" };
}

module.exports = { FlagNotFoundError, evaluateFlag };
