"use strict";
Object.defineProperty(exports, "__esModule", { value: true });

const { buildSnapshot, closureDigest, ConfigError } = require("./snapshot");
const { evaluateFlag } = require("./evaluator");

function cacheKey(snapshot, flagId, user) {
  return [
    `v${snapshot.version}`,
    flagId,
    user.id,
    `attrs@${user.attributesVersion}`,
    closureDigest(snapshot, flagId),
  ].join("|");
}

class FlagService {
  constructor(flags) {
    this.snapshots = new Map();
    this.currentVersion = 0;
    this.cache = new Map();
    if (flags) this.updateConfig(flags);
  }

  updateConfig(flags) {
    const snapshot = buildSnapshot(this.currentVersion + 1, flags, Date.now());
    this.snapshots.set(snapshot.version, snapshot);
    this.currentVersion = snapshot.version;
    return snapshot;
  }

  getSnapshot(version) {
    const v = version ?? this.currentVersion;
    const snapshot = this.snapshots.get(v);
    if (!snapshot) throw new ConfigError(`unknown snapshot version: ${v}`);
    return snapshot;
  }

  listVersions() {
    return [...this.snapshots.keys()].sort((a, b) => a - b);
  }

  evaluate(flagId, user, options = {}) {
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

  evaluateAll(user, options = {}) {
    const snapshot = this.getSnapshot(options.version);
    const out = {};
    for (const flagId of Object.keys(snapshot.flags)) {
      out[flagId] = this.evaluate(flagId, user, { ...options, version: snapshot.version });
    }
    return out;
  }

  cacheKeyFor(flagId, user, version) {
    return cacheKey(this.getSnapshot(version), flagId, user);
  }

  get cacheSize() {
    return this.cache.size;
  }

  clearCache() {
    this.cache.clear();
  }
}

function createUser(id, attributes = {}, attributesVersion = 1) {
  return { id, attributes, attributesVersion };
}

function withAttributes(user, attributes) {
  return { ...user, attributes, attributesVersion: user.attributesVersion + 1 };
}

module.exports = { cacheKey, FlagService, createUser, withAttributes };
