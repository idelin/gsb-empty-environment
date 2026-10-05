/**
 * Abstract data-flow state and transfer functions.
 *
 * Rules (as defined by the problem):
 *  - assign:   taint(x) = union of taint of the right-hand side variables.
 *  - merge:    the analysis is path-sensitive; states from different
 *              predecessors coexist at joins (union of states, no widening).
 *  - sanitize: clears taint of x only when typeof(x) is in the sanitizer's
 *              declared type list; otherwise it has no effect.
 *  - alias:    `p = &x` records a points-to edge; `*p = e` (store) writes
 *              through every possible target. Any store through an alias
 *              invalidates the sanitization certificate of its targets:
 *              a strong update (unique target) replaces the target taint,
 *              a weak update (may-alias) joins it. `x = *p` (load) reads
 *              through every possible target.
 *  - sink:     dangerous when some argument is tainted and its type is in
 *              the sink's dangerTypes (or dangerTypes is omitted).
 */

import { CfgNode, Graph, SanitizerRule, SinkRule, SourceRule } from "./model";

export interface DataState {
  /** var -> tainting source rule ids. Empty/absent means clean. */
  taint: Map<string, Set<string>>;
  /** var -> declared/propagated value type. */
  types: Map<string, string>;
  /** pointer var -> possible pointee variables. */
  alias: Map<string, Set<string>>;
  /** vars whose sanitizer certificate is currently valid. */
  sanitized: Set<string>;
}

export function emptyState(): DataState {
  return { taint: new Map(), types: new Map(), alias: new Map(), sanitized: new Set() };
}

export function cloneState(s: DataState): DataState {
  const taint = new Map<string, Set<string>>();
  for (const [k, v] of s.taint) taint.set(k, new Set(v));
  const alias = new Map<string, Set<string>>();
  for (const [k, v] of s.alias) alias.set(k, new Set(v));
  return {
    taint,
    types: new Map(s.types),
    alias,
    sanitized: new Set(s.sanitized),
  };
}

function setTaint(s: DataState, variable: string, sources: Set<string>): void {
  if (sources.size === 0) s.taint.delete(variable);
  else s.taint.set(variable, new Set(sources));
}

function unionTaintOf(s: DataState, vars: string[]): Set<string> {
  const out = new Set<string>();
  for (const v of vars) {
    const t = s.taint.get(v);
    if (t) for (const id of t) out.add(id);
  }
  return out;
}

function propagateType(s: DataState, vars: string[]): string | undefined {
  for (const v of vars) {
    const t = s.types.get(v);
    if (t !== undefined) return t;
  }
  return undefined;
}

export interface Danger {
  arg: string;
  sources: string[];
  type: string | undefined;
}

export interface TransferResult {
  /** Variables whose condition knowledge must be killed. */
  killed: string[];
  /** Human-readable notes about rule effects on this step. */
  notes: string[];
  /** Non-empty when this node is a dangerous sink call. */
  dangers: Danger[];
  warnings: string[];
}

export function applyTransfer(
  graph: Graph,
  node: CfgNode,
  s: DataState
): TransferResult {
  const killed: string[] = [];
  const notes: string[] = [];
  const dangers: Danger[] = [];
  const warnings: string[] = [];

  const sourceRule = (id: string): SourceRule =>
    graph.rules.sources.find((r) => r.id === id) as SourceRule;
  const sanitizerRule = (id: string): SanitizerRule =>
    graph.rules.sanitizers.find((r) => r.id === id) as SanitizerRule;
  const sinkRule = (id: string): SinkRule =>
    graph.rules.sinks.find((r) => r.id === id) as SinkRule;

  switch (node.op) {
    case "nop":
      break;

    case "source": {
      const rule = sourceRule(node.source as string);
      const variable = node.var as string;
      setTaint(s, variable, new Set([rule.id]));
      s.types.set(variable, rule.type);
      s.sanitized.delete(variable);
      killed.push(variable);
      notes.push(`${variable} tainted by source "${rule.id}" (type ${rule.type})`);
      break;
    }

    case "assign": {
      const variable = node.var as string;
      const expr = node.expr as string[];
      setTaint(s, variable, unionTaintOf(s, expr));
      const t = node.type ?? propagateType(s, expr);
      if (t !== undefined) s.types.set(variable, t);
      else s.types.delete(variable);
      s.sanitized.delete(variable);
      killed.push(variable);
      break;
    }

    case "sanitize": {
      const rule = sanitizerRule(node.sanitizer as string);
      const variable = node.var as string;
      const t = s.types.get(variable);
      if (t !== undefined && rule.types.includes(t)) {
        setTaint(s, variable, new Set());
        s.sanitized.add(variable);
        notes.push(`sanitizer "${rule.id}" cleaned ${variable} (type ${t})`);
      } else {
        notes.push(
          `sanitizer "${rule.id}" ineffective on ${variable} (type ${t ?? "unknown"} not in [${rule.types.join(", ")}])`
        );
      }
      break;
    }

    case "alias": {
      const targets = node.pointsToAny ?? [node.pointsTo as string];
      s.alias.set(node.var as string, new Set(targets));
      break;
    }

    case "store": {
      const pointer = node.pointer as string;
      const expr = node.expr as string[];
      const targets = s.alias.get(pointer);
      if (!targets || targets.size === 0) {
        warnings.push(`store through unknown pointer "${pointer}" ignored`);
        break;
      }
      const rhs = unionTaintOf(s, expr);
      const strong = targets.size === 1;
      for (const target of targets) {
        if (strong) {
          setTaint(s, target, rhs);
        } else if (rhs.size > 0) {
          const cur = s.taint.get(target) ?? new Set<string>();
          setTaint(s, target, new Set([...cur, ...rhs]));
        }
        if (s.sanitized.has(target)) {
          s.sanitized.delete(target);
          notes.push(`alias write through "${pointer}" invalidated sanitization of ${target}`);
        }
        if (s.types.get(target) === undefined) {
          const t = propagateType(s, expr);
          if (t !== undefined) s.types.set(target, t);
        }
        killed.push(target);
      }
      break;
    }

    case "load": {
      const variable = node.var as string;
      const pointer = node.pointer as string;
      const targets = s.alias.get(pointer);
      if (!targets || targets.size === 0) {
        warnings.push(`load through unknown pointer "${pointer}"; ${variable} becomes clean`);
        setTaint(s, variable, new Set());
      } else {
        setTaint(s, variable, unionTaintOf(s, [...targets]));
        const t = node.type ?? propagateType(s, [...targets]);
        if (t !== undefined) s.types.set(variable, t);
        else s.types.delete(variable);
      }
      s.sanitized.delete(variable);
      killed.push(variable);
      break;
    }

    case "sink": {
      const rule = sinkRule(node.sink as string);
      for (const arg of node.args as string[]) {
        const taint = s.taint.get(arg);
        if (!taint || taint.size === 0) continue;
        const t = s.types.get(arg);
        if (rule.dangerTypes !== undefined && (t === undefined || !rule.dangerTypes.includes(t))) {
          continue;
        }
        dangers.push({ arg, sources: [...taint].sort(), type: t });
      }
      break;
    }
  }

  return { killed, notes, dangers, warnings };
}

export function dataStateKey(s: DataState): string {
  const taint = [...s.taint.entries()]
    .map(([v, srcs]) => `${v}:${[...srcs].sort().join("+")}`)
    .sort()
    .join(",");
  const types = [...s.types.entries()].map(([v, t]) => `${v}:${t}`).sort().join(",");
  const alias = [...s.alias.entries()]
    .map(([v, ts]) => `${v}:${[...ts].sort().join("+")}`)
    .sort()
    .join(",");
  const san = [...s.sanitized].sort().join(",");
  return `T[${taint}]Y[${types}]A[${alias}]S[${san}]`;
}
