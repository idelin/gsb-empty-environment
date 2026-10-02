/**
 * Reflection-mode validation engine: offline integrity inspection over a
 * fully decoded message tree. Detects dangling references, orphan cycles,
 * data skew, leaks, and evaluates user-registered formal invariants.
 *
 * The engine is pure with respect to its inputs: `inspect()` keeps all
 * mutable state in a per-run context, so concurrent inspections never
 * interfere (verified by the concurrency test-suite).
 */
import type { Schema, Leak } from './schema.ts';

export type ViolationKind =
  | 'dangling-ref'
  | 'orphan-cycle'
  | 'orphan-node'
  | 'data-skew'
  | 'invariant'
  | 'leak';

export interface Violation {
  kind: ViolationKind;
  path: string;
  message: string;
}

export interface SkewStat {
  field: string;
  samples: number;
  max: number;
  mean: number;
  ratio: number;
}

export interface InstanceInfo {
  path: string;
  schema: string;
  value: Record<string, unknown>;
}

export interface TreeContext {
  root: Record<string, unknown>;
  instances: InstanceInfo[];
}

export interface InspectionReport {
  ok: boolean;
  violations: Violation[];
  skew: SkewStat[];
  nodes: number;
  edges: number;
  unreachable: number;
}

export interface InspectOptions {
  /** max/mean cardinality ratio above which a repeated field is flagged as skewed. */
  skewThreshold?: number;
  /** leaks reported by the decoder (unconsumed sub-message bytes). */
  leaks?: Leak[];
}

interface PendingEdge {
  from: string;
  entity: string;
  key: string;
  via: string;
}

export class Inspector {
  private schemas = new Map<string, Schema>();
  private invariants: { name: string; check: (ctx: TreeContext) => string | null }[] = [];

  registerSchema(schema: Schema): this {
    this.schemas.set(schema.name, schema);
    return this;
  }

  addInvariant(name: string, check: (ctx: TreeContext) => string | null): this {
    this.invariants.push({ name, check });
    return this;
  }

  inspect(
    rootSchemaName: string,
    root: Record<string, unknown>,
    opts: InspectOptions = {},
  ): InspectionReport {
    const skewThreshold = opts.skewThreshold ?? 3;
    const rootSchema = this.schemas.get(rootSchemaName);
    if (!rootSchema) throw new Error(`schema '${rootSchemaName}' is not registered`);

    const violations: Violation[] = [];
    const instances: InstanceInfo[] = [];
    const keyIndex = new Map<string, Map<string, string>>(); // entity -> key -> path
    const roots: string[] = [];
    const pending: PendingEdge[] = [];
    const repeatedSamples = new Map<string, number[]>();

    const visit = (
      value: Record<string, unknown>,
      schema: Schema,
      path: string,
      isRoot: boolean,
    ): void => {
      instances.push({ path, schema: schema.name, value });
      if (isRoot) roots.push(path);
      for (const def of schema.fields) {
        const raw = value[def.name];
        if (raw === undefined || raw === null) continue;
        if (def.key) {
          let idx = keyIndex.get(schema.name);
          if (!idx) keyIndex.set(schema.name, (idx = new Map()));
          const k = String(raw);
          const prev = idx.get(k);
          if (prev !== undefined) {
            violations.push({
              kind: 'invariant',
              path,
              message: `duplicate key '${k}' for entity ${schema.name} (first seen at ${prev})`,
            });
          } else {
            idx.set(k, path);
          }
        }
        if (def.repeated && Array.isArray(raw)) {
          const label = `${schema.name}.${def.name}`;
          let arr = repeatedSamples.get(label);
          if (!arr) repeatedSamples.set(label, (arr = []));
          arr.push(raw.length);
        }
        if (def.ref) {
          const vals = def.repeated ? (raw as unknown[]) : [raw];
          for (const v of vals) {
            pending.push({ from: path, entity: def.ref.entity, key: String(v), via: def.name });
          }
        }
        if (def.message) {
          const childRoot = def.root === true;
          if (def.repeated) {
            (raw as Record<string, unknown>[]).forEach((child, i) =>
              visit(child, def.message as Schema, `${path}.${def.name}[${i}]`, childRoot),
            );
          } else {
            visit(raw as Record<string, unknown>, def.message, `${path}.${def.name}`, childRoot);
          }
        }
      }
    };
    visit(root, rootSchema, '$', true);

    // Resolve reference edges; unresolved targets are dangling references.
    const adjacency = new Map<string, Set<string>>();
    const addEdge = (a: string, b: string): void => {
      let s = adjacency.get(a);
      if (!s) adjacency.set(a, (s = new Set()));
      s.add(b);
    };
    let edges = 0;
    for (const e of pending) {
      const target = keyIndex.get(e.entity)?.get(e.key);
      if (target === undefined) {
        violations.push({
          kind: 'dangling-ref',
          path: e.from,
          message: `dangling reference via '${e.via}' to ${e.entity}#${e.key}`,
        });
      } else {
        addEdge(e.from, target);
        edges += 1;
      }
    }

    // Reachability over the reference graph, seeded from root-marked collections.
    const reachable = new Set<string>();
    const queue = [...roots];
    for (const r of roots) reachable.add(r);
    while (queue.length > 0) {
      const cur = queue.pop() as string;
      for (const next of adjacency.get(cur) ?? []) {
        if (!reachable.has(next)) {
          reachable.add(next);
          queue.push(next);
        }
      }
    }

    // Orphan analysis on unreachable keyed instances (Tarjan SCC, iterative).
    const pathSet = new Set(instances.map((i) => i.path));
    const unreachable = instances.filter((i) => !reachable.has(i.path)).map((i) => i.path);
    const unreachableSet = new Set(unreachable);
    const cycles = orphanCycles(unreachableSet, adjacency);
    for (const cycle of cycles) {
      violations.push({
        kind: 'orphan-cycle',
        path: cycle[0],
        message: `orphan cycle of ${cycle.length} node(s): ${cycle.join(' -> ')}`,
      });
    }
    const cyclic = new Set(cycles.flat());
    for (const p of unreachable) {
      if (!cyclic.has(p) && pathSet.has(p)) {
        violations.push({
          kind: 'orphan-node',
          path: p,
          message: `unreachable node '${p}' is not referenced by any root-anchored chain`,
        });
      }
    }

    // Data-skew analysis on repeated-field cardinalities.
    const skew: SkewStat[] = [];
    for (const [label, samples] of repeatedSamples) {
      if (samples.length < 2) continue;
      const sum = samples.reduce((a, b) => a + b, 0);
      const mean = sum / samples.length;
      const max = Math.max(...samples);
      const ratio = mean > 0 ? max / mean : 0;
      const stat: SkewStat = { field: label, samples: samples.length, max, mean, ratio };
      skew.push(stat);
      if (max > 0 && ratio >= skewThreshold) {
        violations.push({
          kind: 'data-skew',
          path: label,
          message:
            `data skew on '${label}': max=${max} mean=${mean.toFixed(2)} ` +
            `ratio=${ratio.toFixed(2)} >= threshold ${skewThreshold}`,
        });
      }
    }

    // Decoder-reported leaks (unconsumed sub-message bytes).
    for (const leak of opts.leaks ?? []) {
      violations.push({
        kind: 'leak',
        path: leak.path,
        message: `leak: ${leak.unconsumed}B of sub-message left unconsumed at ${leak.path}`,
      });
    }

    // User-registered formal invariants.
    const ctx: TreeContext = { root, instances };
    for (const inv of this.invariants) {
      const message = inv.check(ctx);
      if (message !== null) {
        violations.push({ kind: 'invariant', path: '$', message: `[${inv.name}] ${message}` });
      }
    }

    return {
      ok: violations.length === 0,
      violations,
      skew,
      nodes: instances.length,
      edges,
      unreachable: unreachable.length,
    };
  }
}

/** Iterative Tarjan SCC over the unreachable subgraph; returns cycles (size>1 or self-loop). */
function orphanCycles(nodes: Set<string>, adjacency: Map<string, Set<string>>): string[][] {
  const index = new Map<string, number>();
  const low = new Map<string, number>();
  const onStack = new Set<string>();
  const stack: string[] = [];
  const cycles: string[][] = [];
  let counter = 0;

  for (const start of nodes) {
    if (index.has(start)) continue;
    const work: { node: string; children: string[]; ci: number }[] = [
      { node: start, children: filtered(start), ci: 0 },
    ];
    index.set(start, counter);
    low.set(start, counter);
    counter += 1;
    stack.push(start);
    onStack.add(start);

    while (work.length > 0) {
      const frame = work[work.length - 1];
      if (frame.ci < frame.children.length) {
        const child = frame.children[frame.ci];
        frame.ci += 1;
        if (!index.has(child)) {
          index.set(child, counter);
          low.set(child, counter);
          counter += 1;
          stack.push(child);
          onStack.add(child);
          work.push({ node: child, children: filtered(child), ci: 0 });
        } else if (onStack.has(child)) {
          low.set(frame.node, Math.min(low.get(frame.node) as number, index.get(child) as number));
        }
      } else {
        work.pop();
        if ((low.get(frame.node) as number) === (index.get(frame.node) as number)) {
          const scc: string[] = [];
          let w: string;
          do {
            w = stack.pop() as string;
            onStack.delete(w);
            scc.push(w);
          } while (w !== frame.node);
          const selfLoop = scc.length === 1 && (adjacency.get(scc[0])?.has(scc[0]) ?? false);
          if (scc.length > 1 || selfLoop) cycles.push(scc);
        }
        if (work.length > 0) {
          const parent = work[work.length - 1].node;
          low.set(parent, Math.min(low.get(parent) as number, low.get(frame.node) as number));
        }
      }
    }
  }
  return cycles;

  function filtered(node: string): string[] {
    return [...(adjacency.get(node) ?? [])].filter((n) => nodes.has(n));
  }
}
