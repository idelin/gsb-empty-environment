"use strict";
Object.defineProperty(exports, "__esModule", { value: true });

const { hashString } = require("./hash");
const { hasOwn } = require("./conditions");

class ConfigError extends Error {
  constructor(message) {
    super(message);
    this.name = "ConfigError";
  }
}

function compareGroups(a, b) {
  if (a.priority !== b.priority) return b.priority - a.priority;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

function validateRollout(rollout, owner) {
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

function normalizeFlag(flag) {
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

function validateDependencies(flags) {
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
  const color = {};
  const stack = [];

  const visit = (id) => {
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

function deepFreeze(value) {
  if (value && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Object.keys(value)) {
      deepFreeze(value[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function buildSnapshot(version, flags, createdAtMs) {
  const byId = {};
  for (const flag of flags) {
    if (hasOwn(byId, flag.id)) {
      throw new ConfigError(`duplicate flag id: "${flag.id}"`);
    }
    byId[flag.id] = normalizeFlag(flag);
  }
  validateDependencies(byId);
  return { version, flags: deepFreeze(byId), createdAtMs };
}

function dependencyClosure(snapshot, flagId) {
  const seen = new Set();
  const visit = (id) => {
    if (seen.has(id)) return;
    seen.add(id);
    const flag = snapshot.flags[id];
    for (const dep of (flag && flag.dependsOn) ?? []) visit(dep.flagId);
  };
  visit(flagId);
  return [...seen].sort();
}

function closureDigest(snapshot, flagId) {
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

module.exports = {
  ConfigError,
  compareGroups,
  buildSnapshot,
  dependencyClosure,
  closureDigest,
};
