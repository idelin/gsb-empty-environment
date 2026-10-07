'use strict';

const fs = require('fs');
const path = require('path');

const { CacheStore } = require('./cache');
const { compileGroup } = require('./compiler');
const {
  parseImportSpecifiers,
  resolveSpecifier,
  toModuleId,
  findStronglyConnectedComponents,
  orderGroupsByDependency,
} = require('./graph');
const { sha256 } = require('./hash');

const CACHE_FORMAT_VERSION = 1;

function discoverSourceFiles(rootDir) {
  const files = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      if (entry.name === 'node_modules' || entry.name.startsWith('.')) continue;
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name.endsWith('.ts') && !entry.name.endsWith('.d.ts')) files.push(full);
    }
  };
  walk(rootDir);
  return files.sort();
}

/**
 * Incremental TypeScript builder with a persistent compilation cache.
 *
 * Cache key for a module group =
 *   hash(compiler version, group sources, direct dependency export signatures)
 *
 * where an export signature is the hash of the module's emitted .d.ts.
 * Consequences:
 *  - Internal implementation changes do not alter the .d.ts, so dependents
 *    keep their cache entries (no recompilation of callers).
 *  - Export changes alter the .d.ts, which changes dependents' keys, and the
 *    invalidation propagates transitively as far as signatures actually change.
 *  - Cyclical modules are compiled as one strongly connected group whose key
 *    covers every member's source.
 *  - Failed compilations are never written to the cache.
 */
class Builder {
  constructor({ ts, rootDir, cacheDir, compilerOptions }) {
    this.ts = ts;
    this.rootDir = path.resolve(rootDir);
    this.cache = new CacheStore(cacheDir);
    this.compilerOptions = compilerOptions;
  }

  build() {
    const ts = this.ts;
    const files = discoverSourceFiles(this.rootDir);
    const fileSet = new Set(files);
    const sources = new Map(files.map((f) => [f, fs.readFileSync(f, 'utf8')]));
    const ids = files.map((f) => toModuleId(f, this.rootDir));
    const fileOf = new Map(files.map((f) => [toModuleId(f, this.rootDir), f]));

    // Dependency graph over internal (relative) imports only.
    const adjacency = new Map();
    for (const file of files) {
      const deps = [];
      for (const spec of parseImportSpecifiers(ts, file, sources.get(file))) {
        const resolved = resolveSpecifier(spec, file, fileSet, this.rootDir);
        if (resolved) deps.push(resolved);
      }
      adjacency.set(toModuleId(file, this.rootDir), deps.sort());
    }

    const sccs = findStronglyConnectedComponents(ids, adjacency);
    const orderedGroups = orderGroupsByDependency(sccs, adjacency);

    const signatures = new Map(); // module id -> export signature hash
    const outputs = new Map(); // module id -> { js, dts, fromCache }
    const failed = new Set();
    const stats = { compiled: [], fromCache: [], failed: [], errors: {} };

    for (const group of orderedGroups) {
      const groupSet = new Set(group);

      // Direct dependencies of the group that live outside it.
      const depSignatures = new Map();
      let blockedByFailure = false;
      for (const id of group) {
        for (const dep of adjacency.get(id)) {
          if (groupSet.has(dep)) continue;
          if (failed.has(dep)) {
            blockedByFailure = true;
          } else {
            depSignatures.set(dep, signatures.get(dep));
          }
        }
      }
      if (blockedByFailure) {
        for (const id of group) {
          failed.add(id);
          stats.failed.push(id);
        }
        continue;
      }

      const key = this.groupKey(group, sources, fileOf, depSignatures);
      const cached = this.cache.get(key);
      if (cached) {
        for (const id of group) {
          const member = cached.members[id];
          signatures.set(id, member.signature);
          outputs.set(id, { js: member.js, dts: member.dts, fromCache: true });
          stats.fromCache.push(id);
        }
        continue;
      }

      const groupFiles = group.map((id) => fileOf.get(id));
      const result = compileGroup(ts, files, groupFiles, this.compilerOptions);
      if (!result.ok) {
        // Failed artifacts are never cached.
        for (const id of group) {
          failed.add(id);
          stats.failed.push(id);
        }
        for (const id of group) {
          stats.errors[id] = result.errors;
        }
        continue;
      }

      const members = {};
      for (const id of group) {
        const member = result.members.get(fileOf.get(id));
        members[id] = member;
        signatures.set(id, member.signature);
        outputs.set(id, { js: member.js, dts: member.dts, fromCache: false });
        stats.compiled.push(id);
      }
      this.cache.set(key, { format: CACHE_FORMAT_VERSION, members });
    }

    stats.compiled.sort();
    stats.fromCache.sort();
    stats.failed.sort();
    return {
      success: stats.failed.length === 0,
      outputs,
      stats,
    };
  }

  groupKey(group, sources, fileOf, depSignatures) {
    const basis = {
      format: CACHE_FORMAT_VERSION,
      compiler: this.ts.version,
      members: group.map((id) => ({ id, source: sha256(sources.get(fileOf.get(id))) })),
      deps: [...depSignatures.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
    };
    return sha256(JSON.stringify(basis));
  }
}

module.exports = { Builder };
