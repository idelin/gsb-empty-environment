/**
 * Selector graph definitions.
 *
 * - `fromState` creates a *reader* selector: it evaluates against the state
 *   root and dynamically tracks the property paths it touches (branch-sensitive).
 * - `createSelector` composes other selectors into a *derived* node; nodes are
 *   shared, so a parameterized family built on the same inputs reuses one
 *   subgraph and each shared input is recomputed at most once per commit.
 * - `createSelectorFamily` caches parameterized selectors. The cache identity
 *   is (parameters, equality version, input dependency ids): entries built
 *   with different params, a different `equalityVersion`, or different input
 *   selectors never share a cache slot.
 */

import { stableStringify } from './paths.ts';

export type Equality<T> = (a: T, b: T) => boolean;

export interface SelectorOptions<T> {
  /** Configurable equality used to suppress no-change propagation. */
  equality?: Equality<T>;
  /**
   * Version tag for the equality function. Bump it when the equality logic
   * changes so caches keyed by the old version are not reused.
   */
  equalityVersion?: string;
}

export type SelectorKind = 'reader' | 'derived';

export interface SelectorNodeDef {
  readonly id: number;
  readonly kind: SelectorKind;
  readonly reader: ((state: never) => unknown) | null;
  readonly inputs: Selector<unknown>[];
  readonly combiner: ((...args: never[]) => unknown) | null;
  readonly equality: Equality<unknown>;
  readonly equalityVersion: string;
  readonly depth: number;
}

const NODE: unique symbol = Symbol('projection-store.selectorNode');

export interface Selector<T> {
  readonly [NODE]: SelectorNodeDef;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type AnySelector = Selector<any>;

export function nodeOf<T>(selector: Selector<T>): SelectorNodeDef {
  return selector[NODE];
}

let nextNodeId = 1;

function defineSelector<T>(
  partial: Omit<SelectorNodeDef, 'id'>,
): Selector<T> {
  const def: SelectorNodeDef = { ...partial, id: nextNodeId };
  nextNodeId += 1;
  return { [NODE]: def };
}

const DEFAULT_EQUALITY_VERSION = 'default';

function resolveEquality<T>(options: SelectorOptions<T>): {
  equality: Equality<unknown>;
  equalityVersion: string;
} {
  return {
    equality: (options.equality ?? Object.is) as Equality<unknown>,
    equalityVersion: options.equalityVersion ?? DEFAULT_EQUALITY_VERSION,
  };
}

/** Reader selector over the store state root, with dynamic path tracking. */
export function fromState<S, T>(
  reader: (state: S) => T,
  options: SelectorOptions<T> = {},
): Selector<T> {
  const { equality, equalityVersion } = resolveEquality(options);
  return defineSelector<T>({
    kind: 'reader',
    reader: reader as (state: never) => unknown,
    inputs: [],
    combiner: null,
    equality,
    equalityVersion,
    depth: 1,
  });
}

/** Derived selector over other selectors (shared subgraph node). */
export function createSelector<T>(
  inputs: AnySelector[],
  combiner: (...values: never[]) => T,
  options: SelectorOptions<T> = {},
): Selector<T> {
  if (inputs.length === 0) {
    throw new Error('createSelector requires at least one input selector');
  }
  const { equality, equalityVersion } = resolveEquality(options);
  const depth = 1 + Math.max(...inputs.map((input) => nodeOf(input).depth));
  return defineSelector<T>({
    kind: 'derived',
    reader: null,
    inputs: inputs as Selector<unknown>[],
    combiner: combiner as (...args: never[]) => unknown,
    equality,
    equalityVersion,
    depth,
  });
}

export interface SelectorFamily<P extends unknown[], T> {
  /** Returns the cached selector for these params, building it on first use. */
  for: (...params: P) => Selector<T>;
  /** Number of distinct cache identities currently held. */
  cacheSize: () => number;
  /** Cache keys, for inspection/testing: `params|eq:<version>|in:<inputIds>`. */
  cacheKeys: () => string[];
  clear: () => void;
}

export interface SelectorFamilyOptions<P extends unknown[]> {
  /** Custom parameter key; defaults to a deterministic structural stringify. */
  keyFn?: (...params: P) => string;
}

/**
 * Parameterized selector factory. Every distinct
 * (params, equalityVersion, inputDependencies) triple is a separate cache
 * identity, so selectors for different parameters are never mixed, while
 * identical identities share one node — and therefore one memo cache — in
 * the graph.
 */
export function createSelectorFamily<P extends unknown[], T>(
  factory: (...params: P) => Selector<T>,
  options: SelectorFamilyOptions<P> = {},
): SelectorFamily<P, T> {
  const cache = new Map<string, Selector<T>>();
  const keyFn = options.keyFn ?? ((...params: P) => stableStringify(params));
  return {
    for(...params: P): Selector<T> {
      const paramKey = keyFn(...params);
      const built = factory(...params);
      const def = nodeOf(built);
      const inputIds = def.inputs.map((input) => nodeOf(input).id).join(',');
      const identity = `${paramKey}|eq:${def.equalityVersion}|in:${inputIds}`;
      const existing = cache.get(identity);
      if (existing) return existing;
      cache.set(identity, built);
      return built;
    },
    cacheSize: () => cache.size,
    cacheKeys: () => [...cache.keys()],
    clear: () => cache.clear(),
  };
}
