'use strict';

const fs = require('fs');

/**
 * Cache of compilation artifacts. Entries are scoped per strongly
 * connected component (singleton modules included):
 *   scopeKey (sorted module ids joined by '|') ->
 *   { key, members: { id: { output, signature, signatureHash } } }
 *
 * `key` binds the entry to the exact sources of the scope plus the
 * export-signature hashes of all direct dependencies outside the scope.
 * Failed compilations are never written (builder commits only on success).
 */
class BuildCache {
  constructor() {
    this.entries = new Map();
  }

  get(scopeKey) {
    const entry = this.entries.get(scopeKey);
    if (!entry) return undefined;
    return {
      key: entry.key,
      members: new Map(Object.entries(entry.members)),
    };
  }

  set(scopeKey, entry) {
    this.entries.set(scopeKey, {
      key: entry.key,
      members: Object.fromEntries(entry.members),
    });
  }

  has(scopeKey) {
    return this.entries.has(scopeKey);
  }

  save(file) {
    fs.writeFileSync(file, JSON.stringify([...this.entries], null, 2));
  }

  static load(file) {
    const cache = new BuildCache();
    if (fs.existsSync(file)) {
      cache.entries = new Map(JSON.parse(fs.readFileSync(file, 'utf8')));
    }
    return cache;
  }
}

module.exports = { BuildCache };
