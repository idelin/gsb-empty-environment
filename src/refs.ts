import {
  encodePath,
  getPath,
  isArray,
  isObject,
  type JsonObject,
  type JsonValue,
  type PathSeg,
} from "./json.ts";
import { isRef, refTarget } from "./markers.ts";
import { ConfigError, RefCycleError } from "./errors.ts";
import type { Explain } from "./merge.ts";

/** Field-level reference dependency graph: location path -> target path. */
export class RefGraph {
  /** encoded location -> location segments */
  readonly locations = new Map<string, PathSeg[]>();
  /** encoded location -> target segments */
  readonly targets = new Map<string, PathSeg[]>();

  set(location: PathSeg[], target: PathSeg[]): void {
    const key = encodePath(location);
    this.locations.set(key, [...location]);
    this.targets.set(key, [...target]);
  }

  removeAtOrUnder(path: readonly PathSeg[]): void {
    const prefix = encodePath(path);
    for (const key of [...this.locations.keys()]) {
      if (key === prefix || (prefix === "" ? key !== "" : key.startsWith(prefix + "/"))) {
        this.locations.delete(key);
        this.targets.delete(key);
      }
    }
  }

  clone(): RefGraph {
    const g = new RefGraph();
    for (const [k, v] of this.locations) g.locations.set(k, [...v]);
    for (const [k, v] of this.targets) g.targets.set(k, [...v]);
    return g;
  }
}

/** Collect every pure ref in the tree into `graph`. */
export function collectRefs(value: JsonValue, path: PathSeg[], graph: RefGraph): void {
  if (isRef(value)) {
    graph.set(path, refTarget(value));
    return;
  }
  if (isArray(value)) {
    for (let i = 0; i < value.length; i++) collectRefs(value[i], [...path, i], graph);
    return;
  }
  if (isObject(value)) {
    for (const k of Object.keys(value)) collectRefs(value[k], [...path, k], graph);
  }
}

export interface ResolveResult {
  config: JsonValue;
  /** encoded ref location -> resolved value (cache reusable by incremental compiles) */
  resolved: Map<string, JsonValue>;
  graph: RefGraph;
}

export interface ResolveOptions {
  /** Ref locations allowed to be served from `cache` instead of being re-resolved. */
  cache?: Map<string, JsonValue>;
  /** When set, only these encoded locations are (re)resolved; others must be in cache. */
  only?: Set<string>;
}

/** Resolve all refs in `raw` (the merged, unresolved tree).
 *  Resolution is recursive over the field dependency graph; cycles and dangling
 *  targets raise structured errors locating the exact field path. */
export function resolveRefs(raw: JsonValue, graph: RefGraph, explain: Explain, opts: ResolveOptions = {}): ResolveResult {
  const resolved = new Map<string, JsonValue>();
  const chain: PathSeg[][] = [];

  const resolveRefAt = (location: PathSeg[]): JsonValue => {
    const key = encodePath(location);
    if (resolved.has(key)) return resolved.get(key) as JsonValue;
    if (opts.cache && opts.only && !opts.only.has(key)) {
      const hit = opts.cache.get(key);
      if (hit !== undefined) {
        resolved.set(key, hit);
        return hit;
      }
    }
    if (chain.some((p) => encodePath(p) === key)) {
      const start = chain.findIndex((p) => encodePath(p) === key);
      throw new RefCycleError([...chain.slice(start), [...location]]);
    }
    const target = graph.targets.get(key);
    if (target === undefined) throw new ConfigError("REF_NOT_FOUND", `ref has no registered target`, { path: [...location] });
    const targetValue = getPath(raw, target);
    if (targetValue === undefined) {
      throw new ConfigError("REF_NOT_FOUND", `ref target does not exist`, {
        path: [...location],
        details: { target },
      });
    }
    chain.push([...location]);
    const out = resolveSubtree(targetValue, target);
    chain.pop();
    resolved.set(key, out);
    explain.removeRefEntriesAt(location);
    explain.add(location, { kind: "ref", target: [...target], resolvedFrom: [...location] });
    return out;
  };

  const resolveSubtree = (value: JsonValue, path: PathSeg[]): JsonValue => {
    if (isRef(value)) return resolveRefAt(path);
    if (isArray(value)) return value.map((item, i) => resolveSubtree(item, [...path, i]));
    if (isObject(value)) {
      const out: JsonObject = {};
      for (const k of Object.keys(value)) out[k] = resolveSubtree(value[k], [...path, k]);
      return out;
    }
    return value;
  };

  const config = resolveSubtree(raw, []);
  return { config, resolved, graph };
}
