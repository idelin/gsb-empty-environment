/**
 * Structure-version-bound result cache.
 *
 * Each root object carries a monotonically increasing version. Query results
 * (matches plus their path evidence) are cached under (expression, version).
 * A local change to the structure is announced with `invalidate(root)`, which
 * bumps the version; the next query recomputes and revises both the match
 * set and the recorded evidence paths.
 */
import type { CompiledPlan, Match, RunOptions } from './compiler.ts';

const IDENT = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

/** Format a match path as user-facing evidence, e.g. `$.items[2]["a b"]`. */
export function formatPath(path: readonly (string | number)[]): string {
  let out = '$';
  for (const key of path) {
    if (typeof key === 'number') out += `[${key}]`;
    else if (IDENT.test(key)) out += `.${key}`;
    else out += `[${JSON.stringify(key)}]`;
  }
  return out;
}

export interface CachedResult {
  readonly version: number;
  readonly matches: Match[];
  readonly evidence: string[];
}

export interface QueryOutcome {
  readonly result: CachedResult;
  readonly fromCache: boolean;
}

interface RootState {
  version: number;
  entries: Map<string, CachedResult>;
}

export class StructureCache {
  private readonly states = new WeakMap<object, RootState>();

  private stateFor(root: object): RootState {
    let state = this.states.get(root);
    if (state === undefined) {
      state = { version: 0, entries: new Map() };
      this.states.set(root, state);
    }
    return state;
  }

  /** Current structural version of `root`. */
  versionOf(root: object): number {
    return this.stateFor(root).version;
  }

  /** Announce a local change: bumps the version, revising future results. */
  invalidate(root: object): number {
    const state = this.stateFor(root);
    state.version += 1;
    return state.version;
  }

  /** Run `plan` against `root`, serving from cache when the version matches. */
  query(plan: CompiledPlan, root: object, options: RunOptions = {}): QueryOutcome {
    const state = this.stateFor(root);
    const hit = state.entries.get(plan.expression);
    if (hit !== undefined && hit.version === state.version) {
      return { result: hit, fromCache: true };
    }
    const matches = plan.run(root, options);
    const result: CachedResult = {
      version: state.version,
      matches,
      evidence: matches.map((m) => formatPath(m.path)),
    };
    state.entries.set(plan.expression, result);
    return { result, fromCache: false };
  }
}
