import { PathError } from './errors.ts';
import { compilePath, type Plan } from './plan.ts';
import { FORBIDDEN_KEYS } from './parser.ts';
import {
  assertDataTree,
  assertDataValue,
  isPlainObject,
  readOwn,
} from './safeobj.ts';
import { PlanCursor, type Match, type PathSeg } from './vm.ts';

export const DEFAULT_BUDGET = 10_000;

export interface QueryResult {
  readonly matches: Match[];
  readonly version: number;
  readonly fromCache: boolean;
}

interface CacheEntry {
  version: number;
  matches: Match[];
  /** Concrete paths read while producing `matches` (path evidence). */
  reads: PathSeg[][];
  /** True when read recording overflowed: invalidate on any change. */
  truncated: boolean;
}

function isPrefixPath(prefix: readonly PathSeg[], full: readonly PathSeg[]): boolean {
  if (prefix.length > full.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== full[i]) return false;
  }
  return true;
}

function pathsOverlap(a: readonly PathSeg[], b: readonly PathSeg[]): boolean {
  return isPrefixPath(a, b) || isPrefixPath(b, a);
}

/**
 * A versioned document store with a structure-version-bound query cache.
 *
 * Every mutation goes through set()/remove(), which bumps the version and
 * records the changed path. Cached query results carry the concrete read
 * paths observed during evaluation; a local change only invalidates entries
 * whose evidence overlaps the changed path, so unrelated queries survive.
 */
export class DocumentStore {
  private root: unknown;
  private ver = 0;
  private readonly plans = new Map<string, Plan>();
  private readonly cache = new Map<string, CacheEntry>();

  constructor(root: unknown) {
    assertDataTree(root, '(document)');
    this.root = root;
  }

  get version(): number {
    return this.ver;
  }

  get document(): unknown {
    return this.root;
  }

  get cacheSize(): number {
    return this.cache.size;
  }

  /** Replace the value at `path` (array of keys/indices). */
  set(path: readonly PathSeg[], value: unknown): void {
    assertDataTree(value, '(value)');
    const { parent, key } = this.navigate(path, true);
    if (FORBIDDEN_KEYS.has(String(key))) {
      throw new PathError(
        'FORBIDDEN_PROPERTY',
        '(document)',
        `access to property ${JSON.stringify(String(key))} is denied`,
      );
    }
    if (Array.isArray(parent)) {
      if (typeof key !== 'number' || key < 0 || key > parent.length) {
        throw new PathError('TYPE_ERROR', '(document)', 'invalid array index in set');
      }
      parent[key] = value;
    } else if (isPlainObject(parent)) {
      (parent as Record<string, unknown>)[String(key)] = value;
    } else {
      throw new PathError('TYPE_ERROR', '(document)', 'cannot write into a scalar');
    }
    this.afterMutation(path);
  }

  /** Delete the value at `path`. Missing targets are a no-op. */
  remove(path: readonly PathSeg[]): void {
    if (path.length === 0) {
      throw new PathError('TYPE_ERROR', '(document)', 'cannot remove the root');
    }
    const { parent, key } = this.navigate(path, false);
    if (Array.isArray(parent) && typeof key === 'number') {
      if (key >= 0 && key < parent.length) {
        parent.splice(key, 1);
        this.afterMutation(path);
      }
      return;
    }
    if (isPlainObject(parent)) {
      const name = String(key);
      if (FORBIDDEN_KEYS.has(name)) {
        throw new PathError(
          'FORBIDDEN_PROPERTY',
          '(document)',
          `access to property ${JSON.stringify(name)} is denied`,
        );
      }
      if (Object.prototype.hasOwnProperty.call(parent, name)) {
        delete (parent as Record<string, unknown>)[name];
        this.afterMutation(path);
      }
    }
  }

  private navigate(
    path: readonly PathSeg[],
    create: boolean,
  ): { parent: unknown; key: PathSeg } {
    if (path.length === 0) {
      throw new PathError('TYPE_ERROR', '(document)', 'empty mutation path');
    }
    let cur = this.root;
    for (let i = 0; i < path.length - 1; i++) {
      const seg = path[i]!;
      const r = readOwn(cur, String(seg), '(document)');
      if (r.found) {
        cur = r.value;
      } else if (create) {
        if (!isPlainObject(cur)) {
          throw new PathError('TYPE_ERROR', '(document)', 'cannot create inside non-object');
        }
        const fresh: Record<string, unknown> = {};
        (cur as Record<string, unknown>)[String(seg)] = fresh;
        cur = fresh;
      } else {
        return { parent: undefined, key: path[path.length - 1]! };
      }
    }
    return { parent: cur, key: path[path.length - 1]! };
  }

  private afterMutation(path: readonly PathSeg[]): void {
    this.ver++;
    // Local-change revision: only entries whose path evidence overlaps the
    // mutated path are dropped; everything else stays valid.
    for (const [source, entry] of this.cache) {
      if (
        entry.truncated ||
        // The empty (root) read is shared by every query and every
        // mutation; it carries no invalidation signal on its own.
        entry.reads.some((read) => read.length > 0 && pathsOverlap(read, path))
      ) {
        this.cache.delete(source);
      }
    }
  }

  /**
   * Run a path query against the current version. Results are cached per
   * (expression, structure version) with evidence-based invalidation.
   */
  query(source: string, budget = DEFAULT_BUDGET): QueryResult {
    const hit = this.cache.get(source);
    // Entries survive version bumps unless their path evidence overlapped a
    // mutation (see afterMutation), so a surviving entry is still valid.
    if (hit) {
      return { matches: hit.matches, version: this.ver, fromCache: true };
    }
    let plan = this.plans.get(source);
    if (!plan) {
      plan = compilePath(source);
      this.plans.set(source, plan);
    }
    const cursor = new PlanCursor(plan, this.root, budget);
    cursor.run();
    const matches = cursor.takeMatches();
    this.cache.set(source, {
      version: this.ver,
      matches,
      reads: cursor.reads,
      truncated: cursor.readsTruncated,
    });
    return { matches, version: this.ver, fromCache: false };
  }
}

/** One-shot convenience API: parse, compile and run within `budget`. */
export function queryOnce(
  root: unknown,
  source: string,
  budget = DEFAULT_BUDGET,
): Match[] {
  assertDataValue(root, source);
  const plan = compilePath(source);
  const cursor = new PlanCursor(plan, root, budget);
  cursor.run();
  return cursor.takeMatches();
}
