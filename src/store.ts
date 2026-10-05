/**
 * Version store: hands out opaque handles for map snapshots and lets the
 * owner release versions that are no longer needed.
 *
 * Diff cursors reference versions by handle, not by root pointer, so
 * resuming a diff against a released version fails loudly
 * (`VersionReleasedError`) instead of silently reading stale memory.
 */

import type { PMap } from './avl.ts';

export type Version = number;

export class VersionReleasedError extends Error {
  readonly version: Version;

  constructor(version: Version) {
    super(`pmap-diff: version ${version} has been released or never existed`);
    this.name = 'VersionReleasedError';
    this.version = version;
  }
}

export class VersionStore<K, V> {
  private readonly snapshots = new Map<Version, PMap<K, V>>();
  private nextId = 1;

  /** Pin a snapshot and return its handle. */
  commit(map: PMap<K, V>): Version {
    const version = this.nextId++;
    this.snapshots.set(version, map);
    return version;
  }

  /** Drop a snapshot. Any live diff cursor bound to it will fail on use. */
  release(version: Version): void {
    this.snapshots.delete(version);
  }

  has(version: Version): boolean {
    return this.snapshots.has(version);
  }

  /** Resolve a handle, throwing `VersionReleasedError` if it is gone. */
  resolve(version: Version): PMap<K, V> {
    const map = this.snapshots.get(version);
    if (map === undefined) throw new VersionReleasedError(version);
    return map;
  }
}
