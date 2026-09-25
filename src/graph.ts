import type { Mat3, Vec3 } from './types.ts';
import { apply, invert, multiply } from './matrix.ts';
import { adaptationMatrix, whitesEqual } from './adaptation.ts';
import type { ColorSpace } from './spaces.ts';

export type Transform = (v: Vec3) => Vec3;

export interface EdgeSpec {
  from: string;
  to: string;
  /**
   * 'auto'   - derived from the two space definitions
   *            (decode transfer -> rgbToXYZ -> white adaptation -> inv(rgbToXYZ) -> encode transfer).
   * 'matrix' - an explicit user-supplied 3x3 matrix edge.
   */
  kind?: 'auto' | 'matrix';
  matrix?: Mat3;
  /**
   * 'auto' (default) inserts Bradford adaptation when the white points differ.
   * 'none' forbids adaptation; compilation fails if the whites actually differ.
   */
  adaptation?: 'auto' | 'none';
  bidirectional?: boolean;
  label?: string;
}

export interface GraphConfig {
  spaces: ColorSpace[];
  edges: EdgeSpec[];
}

export class SingularMatrixError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'SingularMatrixError';
  }
}

export class CycleContradictionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CycleContradictionError';
  }
}

export class AdaptationRequiredError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'AdaptationRequiredError';
  }
}

export class UnknownSpaceError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnknownSpaceError';
  }
}

interface CompiledEdge {
  from: string;
  to: string;
  forward: Transform;
  inverse: Transform;
  label: string;
}

export interface ConversionPlan {
  /** Content hash of the graph config; cache key. */
  version: string;
  convert(fromId: string, toId: string, v: Vec3): Vec3;
  pathLabels(fromId: string, toId: string): string[];
}

const PROBES: Vec3[] = [
  [0, 0, 0],
  [1, 1, 1],
  [1, 0, 0],
  [0, 1, 0],
  [0, 0, 1],
  [0.5, 0.5, 0.5],
  [0.25, 0.5, 0.75],
  [0.9, 0.1, 0.3],
];

export function compileGraph(
  config: GraphConfig,
  opts: { tolerance?: number } = {},
): ConversionPlan {
  const tolerance = opts.tolerance ?? 1e-6;
  const spaces = new Map<string, ColorSpace>();
  for (const s of config.spaces) {
    if (spaces.has(s.id)) throw new UnknownSpaceError(`duplicate space id: ${s.id}`);
    spaces.set(s.id, s);
    if (invert(s.rgbToXYZ) === null) {
      throw new SingularMatrixError(`space "${s.id}" has a singular rgbToXYZ matrix`);
    }
  }

  const adj = new Map<string, CompiledEdge[]>();
  const addEdge = (e: CompiledEdge) => {
    const list = adj.get(e.from) ?? [];
    list.push(e);
    adj.set(e.from, list);
  };

  for (const spec of config.edges) {
    const a = spaces.get(spec.from);
    const b = spaces.get(spec.to);
    if (!a) throw new UnknownSpaceError(`edge references unknown space "${spec.from}"`);
    if (!b) throw new UnknownSpaceError(`edge references unknown space "${spec.to}"`);
    const kind = spec.kind ?? 'auto';
    const label = spec.label ?? `${spec.from}->${spec.to}(${kind})`;
    let forward: Transform;
    let inverse: Transform;

    if (kind === 'matrix') {
      if (!spec.matrix) {
        throw new SingularMatrixError(`edge ${label}: missing matrix`);
      }
      const m = spec.matrix;
      const mInv = invert(m);
      if (!mInv) {
        throw new SingularMatrixError(`edge ${label}: matrix is singular, edge is not invertible`);
      }
      forward = (v) => apply(m, v);
      inverse = (v) => apply(mInv, v);
    } else {
      const sameWhite = whitesEqual(a.white, b.white);
      let m: Mat3;
      const bInv = invert(b.rgbToXYZ);
      if (!bInv) throw new SingularMatrixError(`space "${b.id}" has a singular rgbToXYZ matrix`);
      if (!sameWhite) {
        if (spec.adaptation === 'none') {
          throw new AdaptationRequiredError(
            `edge ${label}: white points differ (${a.white} vs ${b.white}); adaptation must not be omitted`,
          );
        }
        m = multiply(bInv, multiply(adaptationMatrix(a.white, b.white), a.rgbToXYZ));
      } else {
        m = multiply(bInv, a.rgbToXYZ);
      }
      const mInv = invert(m);
      if (!mInv) {
        throw new SingularMatrixError(`edge ${label}: composed matrix is singular`);
      }
      const decodeA = a.transfer.decode;
      const encodeA = a.transfer.encode;
      const decodeB = b.transfer.decode;
      const encodeB = b.transfer.encode;
      forward = (v) => {
        const l = apply(m, [decodeA(v[0]), decodeA(v[1]), decodeA(v[2])]);
        return [encodeB(l[0]), encodeB(l[1]), encodeB(l[2])];
      };
      inverse = (v) => {
        const l = apply(mInv, [decodeB(v[0]), decodeB(v[1]), decodeB(v[2])]);
        return [encodeA(l[0]), encodeA(l[1]), encodeA(l[2])];
      };
    }

    addEdge({ from: spec.from, to: spec.to, forward, inverse, label });
    if (spec.bidirectional ?? true) {
      addEdge({ from: spec.to, to: spec.from, forward: inverse, inverse: forward, label: `${label} [reverse]` });
    }
  }

  checkConsistency(adj, [...spaces.keys()], tolerance);

  const version = versionOfConfig(config);
  return {
    version,
    convert(fromId, toId, v) {
      if (fromId === toId) return [v[0], v[1], v[2]];
      const path = findPath(adj, fromId, toId);
      if (!path) throw new UnknownSpaceError(`no conversion path ${fromId} -> ${toId}`);
      let out: Vec3 = [v[0], v[1], v[2]];
      for (const e of path) out = e.forward(out);
      return out;
    },
    pathLabels(fromId, toId) {
      if (fromId === toId) return [];
      const path = findPath(adj, fromId, toId);
      if (!path) throw new UnknownSpaceError(`no conversion path ${fromId} -> ${toId}`);
      return path.map((e) => e.label);
    },
  };
}

function findPath(adj: Map<string, CompiledEdge[]>, from: string, to: string): CompiledEdge[] | null {
  const prev = new Map<string, CompiledEdge>();
  const seen = new Set<string>([from]);
  const queue: string[] = [from];
  while (queue.length > 0) {
    const node = queue.shift() as string;
    if (node === to) break;
    for (const e of adj.get(node) ?? []) {
      if (seen.has(e.to)) continue;
      seen.add(e.to);
      prev.set(e.to, e);
      queue.push(e.to);
    }
  }
  if (!seen.has(to)) return null;
  const path: CompiledEdge[] = [];
  let cur = to;
  while (cur !== from) {
    const e = prev.get(cur) as CompiledEdge;
    path.unshift(e);
    cur = e.from;
  }
  return path;
}

function enumeratePaths(
  adj: Map<string, CompiledEdge[]>,
  from: string,
  to: string,
  maxPaths = 16,
): CompiledEdge[][] {
  const paths: CompiledEdge[][] = [];
  const stack: CompiledEdge[] = [];
  const visited = new Set<string>([from]);
  const dfs = (node: string): void => {
    if (paths.length >= maxPaths) return;
    if (node === to) {
      paths.push([...stack]);
      return;
    }
    for (const e of adj.get(node) ?? []) {
      if (visited.has(e.to)) continue;
      visited.add(e.to);
      stack.push(e);
      dfs(e.to);
      stack.pop();
      visited.delete(e.to);
    }
  };
  dfs(from);
  return paths;
}

function compose(path: CompiledEdge[]): Transform {
  return (v) => {
    let out = v;
    for (const e of path) out = e.forward(out);
    return out;
  };
}

/**
 * Cycle / multi-path consistency: for every ordered pair of nodes, every
 * simple path between them must agree on a set of probe points. Two paths
 * that disagree beyond the tolerance are a contradiction and abort compile.
 */
function checkConsistency(adj: Map<string, CompiledEdge[]>, ids: string[], tolerance: number): void {
  for (const from of ids) {
    for (const to of ids) {
      if (from === to) continue;
      const paths = enumeratePaths(adj, from, to);
      if (paths.length < 2) continue;
      const reference = compose(paths[0]);
      for (let i = 1; i < paths.length; i++) {
        const candidate = compose(paths[i]);
        for (const probe of PROBES) {
          const expected = reference(probe);
          const actual = candidate(probe);
          for (let c = 0; c < 3; c++) {
            const diff = Math.abs(expected[c] - actual[c]);
            if (!(diff <= tolerance)) {
              const describe = (p: CompiledEdge[]) => p.map((e) => e.label).join(' then ');
              throw new CycleContradictionError(
                `contradictory paths ${from} -> ${to}: ` +
                  `[${describe(paths[0])}] vs [${describe(paths[i])}] ` +
                  `differ by ${diff} at probe ${probe}`,
              );
            }
          }
        }
      }
    }
  }
}

export function versionOfConfig(config: GraphConfig): string {
  const s = stableStringify(config);
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return (h >>> 0).toString(16).padStart(8, '0');
}

function stableStringify(x: unknown): string {
  if (Array.isArray(x)) return `[${x.map(stableStringify).join(',')}]`;
  if (x !== null && typeof x === 'object') {
    const keys = Object.keys(x as Record<string, unknown>).sort();
    const body = keys
      .map((k) => `${JSON.stringify(k)}:${stableStringify((x as Record<string, unknown>)[k])}`)
      .join(',');
    return `{${body}}`;
  }
  if (typeof x === 'function') return '"<fn>"';
  return JSON.stringify(x) ?? 'null';
}
