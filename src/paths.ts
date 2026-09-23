/**
 * Path utilities: immutable updates, structural diffs and read tracking.
 * A "path" is a tuple of object keys / array indices from the state root.
 */

export type Path = readonly (string | number)[];

export function pathKey(path: Path): string {
  return JSON.stringify(path);
}

export function isPrefixPath(prefix: Path, path: Path): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i += 1) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}

/** Two paths "intersect" when one is a prefix of the other (same subtree). */
export function pathsIntersect(a: Path, b: Path): boolean {
  return isPrefixPath(a, b) || isPrefixPath(b, a);
}

export function getAtPath(root: unknown, path: Path): unknown {
  let current = root;
  for (const key of path) {
    if (current === null || typeof current !== 'object') return undefined;
    current = (current as Record<string | number, unknown>)[key];
  }
  return current;
}

/** Copy-on-write update; returns a new root, sharing untouched branches. */
export function setAtPathImmutable<T>(root: T, path: Path, value: unknown): T {
  if (path.length === 0) return value as T;
  const [head, ...rest] = path;
  const source: Record<string | number, unknown> =
    root !== null && typeof root === 'object'
      ? (root as Record<string | number, unknown>)
      : {};
  const clone: Record<string | number, unknown> = Array.isArray(source)
    ? (source as unknown[]).slice() as Record<string | number, unknown>
    : { ...source };
  clone[head] = setAtPathImmutable(source[head], rest, value);
  return clone as T;
}

/**
 * Structural diff: pushes the leaf paths whose values differ between `a` and `b`
 * (reference-unequal objects are recursed; reference-equal subtrees are skipped).
 * Array length changes additionally record a `<path>.length` entry so readers of
 * `.length` are invalidated even when indices themselves look untouched.
 */
export function diffPaths(a: unknown, b: unknown, base: (string | number)[], out: Path[]): void {
  if (Object.is(a, b)) return;
  const aIsObj = a !== null && typeof a === 'object';
  const bIsObj = b !== null && typeof b === 'object';
  if (!aIsObj || !bIsObj || Array.isArray(a) !== Array.isArray(b)) {
    out.push(base);
    return;
  }
  const aRec = a as Record<string, unknown>;
  const bRec = b as Record<string, unknown>;
  const keys = new Set([...Object.keys(aRec), ...Object.keys(bRec)]);
  for (const key of keys) {
    diffPaths(aRec[key], bRec[key], [...base, key], out);
  }
  if (Array.isArray(a) && Array.isArray(b) && a.length !== b.length) {
    out.push([...base, 'length']);
  }
}

export interface ReadTracker {
  proxy: unknown;
  paths: Path[];
  /** Deeply replaces tracked proxies with their raw targets (reference-preserving). */
  unwrap: (value: unknown) => unknown;
}

/**
 * Wraps `root` in lazy proxies that record every property path read while a
 * selector evaluates. The recorded set is the selector's dynamic dependency
 * set: re-evaluation replaces it wholesale, which is what "revokes" stale
 * dependencies after a conditional branch switch.
 */
export function createReadTracker(root: unknown): ReadTracker {
  const paths: Path[] = [];
  const seen = new Set<string>();
  const record = (path: Path): void => {
    const key = pathKey(path);
    if (!seen.has(key)) {
      seen.add(key);
      paths.push(path);
    }
  };
  const proxyToRaw = new Map<unknown, unknown>();
  const wrap = (target: unknown, path: (string | number)[]): unknown => {
    if (target === null || typeof target !== 'object') return target;
    // The proxy wraps an empty shell (not the target itself) so that frozen
    // targets do not trip proxy invariants on non-configurable properties,
    // and stray writes through the proxy land on the shell, never on state.
    const shell: object = Array.isArray(target) ? [] : {};
    const real = target as Record<string | number | symbol, unknown>;
    const proxy = new Proxy(shell, {
      get(_shell, prop) {
        const value = Reflect.get(real, prop);
        if (typeof prop === 'symbol') return value;
        record([...path, prop]);
        return wrap(value, [...path, prop]);
      },
      has(_shell, prop) {
        if (typeof prop !== 'symbol') record([...path, prop as string]);
        return Reflect.has(real, prop);
      },
      ownKeys(_shell) {
        record(path.length === 0 ? [''] : path);
        return Reflect.ownKeys(real);
      },
      getOwnPropertyDescriptor(sh, prop) {
        const shellDesc = Reflect.getOwnPropertyDescriptor(sh, prop);
        if (shellDesc && !shellDesc.configurable) return shellDesc;
        const desc = Reflect.getOwnPropertyDescriptor(real, prop);
        if (!desc) return undefined;
        return { ...desc, configurable: true };
      },
      getPrototypeOf(_shell) {
        return Object.getPrototypeOf(real);
      },
    });
    proxyToRaw.set(proxy, target);
    return proxy;
  };
  const unwrapDeep = (value: unknown, memo: Map<unknown, unknown>): unknown => {
    const raw = proxyToRaw.get(value);
    if (raw !== undefined) return raw;
    if (value === null || typeof value !== 'object') return value;
    const cached = memo.get(value);
    if (cached !== undefined) return cached;
    const rec = value as Record<string, unknown>;
    let clone: Record<string, unknown> | null = null;
    for (const key of Object.keys(rec)) {
      const child = rec[key];
      const unwrapped = unwrapDeep(child, memo);
      if (unwrapped !== child) {
        if (!clone) {
          clone = Array.isArray(value)
            ? ([...(value as unknown[])] as unknown as Record<string, unknown>)
            : { ...rec };
        }
        clone[key] = unwrapped;
      }
    }
    const result = clone ?? value;
    memo.set(value, result);
    return result;
  };
  const unwrap = (value: unknown): unknown => unwrapDeep(value, new Map());
  return { proxy: wrap(root, []), paths, unwrap };
}

/** Deep freeze used by the `freeze` store option to protect mutable projections. */
export function deepFreeze<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value;
  if (Object.isFrozen(value)) return value;
  for (const key of Object.keys(value as Record<string, unknown>)) {
    deepFreeze((value as Record<string, unknown>)[key]);
  }
  return Object.freeze(value);
}

/** Deterministic stringify used for parameterized-selector cache keys. */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'undefined';
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(',')}]`;
  const rec = value as Record<string, unknown>;
  const body = Object.keys(rec)
    .sort()
    .map((key) => `${JSON.stringify(key)}:${stableStringify(rec[key])}`)
    .join(',');
  return `{${body}}`;
}
