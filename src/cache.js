'use strict';

const fs = require('fs');
const path = require('path');

/**
 * Content-addressed, file-backed cache for successful compilation artifacts.
 * Callers must only store successful results; failed compilations must never
 * reach `set` (the builder enforces this invariant).
 */
class CacheStore {
  constructor(dir) {
    this.dir = dir;
    fs.mkdirSync(dir, { recursive: true });
  }

  entryPath(key) {
    return path.join(this.dir, `${key}.json`);
  }

  has(key) {
    return fs.existsSync(this.entryPath(key));
  }

  get(key) {
    const file = this.entryPath(key);
    if (!fs.existsSync(file)) return undefined;
    return JSON.parse(fs.readFileSync(file, 'utf8'));
  }

  set(key, value) {
    const file = this.entryPath(key);
    const tmp = `${file}.tmp-${process.pid}`;
    fs.writeFileSync(tmp, JSON.stringify(value));
    fs.renameSync(tmp, file);
  }

  keys() {
    return fs
      .readdirSync(this.dir)
      .filter((name) => name.endsWith('.json'))
      .map((name) => name.slice(0, -'.json'.length))
      .sort();
  }
}

module.exports = { CacheStore };
