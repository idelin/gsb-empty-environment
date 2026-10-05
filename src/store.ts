/**
 * Versioned store for persistent ordered maps.
 *
 * A committed version is just a retained root node. `release` drops the
 * store's reference; cursors created earlier keep their own references to
 * both roots, so an in-flight diff survives the release of the versions it
 * compares. Asking the store for a released version is a defined error.
 */

import {
  makeConfig,
  get as treapGet,
  set as treapSet,
  remove as treapRemove,
  entries as treapEntries,
  sizeOf,
} from './treap.ts';
import type { TreapConfig, TreapNode } from './treap.ts';
import { Differ } from './diff.ts';
import type { DiffOptions } from './diff.ts';

export class VersionError extends Error {
  constructor(version: number) {
    super(`version ${version} is unknown or has been released`);
    this.name = 'VersionError';
  }
}

export class MapStore<K, V> {
  private readonly cfg: TreapConfig<K, V>;
  private readonly versions = new Map<number, TreapNode<K, V> | null>();
  private nextVersion = 1;

  constructor(config: Partial<TreapConfig<K, V>> = {}) {
    this.cfg = makeConfig(config);
  }

  get config(): TreapConfig<K, V> {
    return this.cfg;
  }

  empty(): TreapNode<K, V> | null {
    return null;
  }

  get(t: TreapNode<K, V> | null, key: K): V | undefined {
    return treapGet(this.cfg, t, key);
  }

  set(t: TreapNode<K, V> | null, key: K, value: V): TreapNode<K, V> | null {
    return treapSet(this.cfg, t, key, value);
  }

  remove(t: TreapNode<K, V> | null, key: K): TreapNode<K, V> | null {
    return treapRemove(this.cfg, t, key);
  }

  entries(t: TreapNode<K, V> | null): Generator<readonly [K, V]> {
    return treapEntries(t);
  }

  size(t: TreapNode<K, V> | null): number {
    return sizeOf(t);
  }

  /** Pins a root as a numbered version. */
  commit(root: TreapNode<K, V> | null): number {
    const id = this.nextVersion++;
    this.versions.set(id, root);
    return id;
  }

  has(version: number): boolean {
    return this.versions.has(version);
  }

  /** Returns the root of a live version; throws VersionError after release. */
  root(version: number): TreapNode<K, V> | null {
    if (!this.versions.has(version)) throw new VersionError(version);
    const root = this.versions.get(version);
    return root === undefined ? null : root;
  }

  /** Drops the store's reference to a version. Returns false if unknown. */
  release(version: number): boolean {
    return this.versions.delete(version);
  }

  differ(options: DiffOptions<V> = {}): Differ<K, V> {
    return new Differ(this.cfg, options);
  }
}
