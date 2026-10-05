import type { Constraint } from "./types.ts";
import { formatConstraint } from "./types.ts";
import { comboBit, isValidConfig } from "./solver.ts";

export interface UnreachableCombo {
  i: number;
  j: number;
  vi: number;
  vj: number;
  kind: "direct" | "propagation" | "exhaustive";
  explanation: string;
  constraintsUsed: Constraint[];
}

// Literal encoding: feature * 2 + value.
const lit = (f: number, v: number): number => f * 2 + v;

interface Implication {
  to: number;
  constraint: Constraint;
}

function buildImplications(constraints: Constraint[]): Map<number, Implication[]> {
  const imps = new Map<number, Implication[]>();
  const add = (from: number, to: number, c: Constraint): void => {
    let arr = imps.get(from);
    if (!arr) imps.set(from, (arr = []));
    arr.push({ to, constraint: c });
  };
  for (const c of constraints) {
    if (c.type === "mutex") {
      add(lit(c.a, 1), lit(c.b, 0), c);
      add(lit(c.b, 1), lit(c.a, 0), c);
    } else {
      add(lit(c.from, 1), lit(c.to, 1), c);
      add(lit(c.to, 0), lit(c.from, 0), c);
    }
  }
  return imps;
}

interface Parent {
  from: number;
  constraint: Constraint;
}

interface PropagationResult {
  assign: number[];
  parent: (Parent | null)[];
  conflict: null | { feature: number; newLit: number };
}

/**
 * Unit propagation over the implication graph. mutex/implies constraints are
 * Horn clauses, so unit propagation is refutation-complete: a combination is
 * unreachable iff propagation derives a contradiction.
 */
function propagate(
  n: number,
  imps: Map<number, Implication[]>,
  seeds: number[],
): PropagationResult {
  const assign: number[] = new Array(n).fill(-1);
  const parent: (Parent | null)[] = new Array(2 * n).fill(null);
  const queue: number[] = [];
  let conflict: PropagationResult["conflict"] = null;

  const setLit = (L: number, from: number, constraint: Constraint | null): boolean => {
    const f = L >> 1;
    const v = L & 1;
    if (assign[f] === v) return true;
    if (assign[f] === 1 - v) return false;
    assign[f] = v;
    if (from >= 0 && constraint) parent[L] = { from, constraint };
    queue.push(L);
    return true;
  };

  for (const s of seeds) {
    if (!setLit(s, -1, null)) {
      conflict = { feature: s >> 1, newLit: s };
      return { assign, parent, conflict };
    }
  }
  while (queue.length > 0 && !conflict) {
    const L = queue.shift()!;
    for (const imp of imps.get(L) ?? []) {
      if (!setLit(imp.to, L, imp.constraint)) {
        // Record the failed derivation so the conflict chain can be rendered.
        parent[imp.to] = { from: L, constraint: imp.constraint };
        conflict = { feature: imp.to >> 1, newLit: imp.to };
        break;
      }
    }
  }
  return { assign, parent, conflict };
}

function renderChain(
  targetLit: number,
  parent: (Parent | null)[],
  names: string[],
): string[] {
  const steps: string[] = [];
  let L: number | null = targetLit;
  while (L !== null) {
    const f = L >> 1;
    const v = L & 1;
    const p = parent[L];
    if (p === null || p === undefined) {
      steps.push(`${names[f]}=${v}（给定）`);
      L = null;
    } else {
      steps.push(`${names[f]}=${v} ［${formatConstraint(p.constraint, names)}］`);
      L = p.from;
    }
  }
  return steps.reverse();
}

function directViolation(
  i: number,
  j: number,
  vi: number,
  vj: number,
  constraints: Constraint[],
  names: string[],
): UnreachableCombo | null {
  for (const c of constraints) {
    if (c.type === "mutex") {
      const match =
        ((c.a === i && c.b === j) || (c.a === j && c.b === i)) && vi === 1 && vj === 1;
      if (match) {
        return {
          i, j, vi, vj, kind: "direct", constraintsUsed: [c],
          explanation: `直接违反约束 ${formatConstraint(c, names)}：两者不能同时取 1`,
        };
      }
    } else {
      const forward = c.from === i && c.to === j && vi === 1 && vj === 0;
      const backward = c.from === j && c.to === i && vj === 1 && vi === 0;
      if (forward || backward) {
        const fv = forward ? vi : vj;
        const tv = forward ? vj : vi;
        return {
          i, j, vi, vj, kind: "direct", constraintsUsed: [c],
          explanation:
            `直接违反约束 ${formatConstraint(c, names)}：` +
            `${names[c.from]}=${fv} 要求 ${names[c.to]}=1，但实际为 ${tv}`,
        };
      }
    }
  }
  return null;
}

/** Greedy deletion: shrink to a minimal constraint subset keeping the combo unreachable. */
function minimalSubset(
  n: number,
  i: number,
  j: number,
  vi: number,
  vj: number,
  constraints: Constraint[],
): Constraint[] {
  const reachableUnder = (cs: Constraint[]): boolean => {
    for (let c = 0; c < 1 << n; c++) {
      if (((c >> i) & 1) !== vi || ((c >> j) & 1) !== vj) continue;
      if (isValidConfig(c, cs)) return true;
    }
    return false;
  };
  let current = [...constraints];
  for (const c of [...current]) {
    const trial = current.filter((x) => x !== c);
    if (!reachableUnder(trial)) current = trial;
  }
  return current;
}

export function explainUnreachable(
  n: number,
  constraints: Constraint[],
  reachable: bigint,
  names: string[],
): UnreachableCombo[] {
  const imps = buildImplications(constraints);
  const out: UnreachableCombo[] = [];
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      for (let vi = 0; vi <= 1; vi++) {
        for (let vj = 0; vj <= 1; vj++) {
          if ((reachable & comboBit(n, i, j, vi, vj)) !== 0n) continue;
          const direct = directViolation(i, j, vi, vj, constraints, names);
          if (direct) {
            out.push(direct);
            continue;
          }
          const prop = propagate(n, imps, [lit(i, vi), lit(j, vj)]);
          if (prop.conflict) {
            const f = prop.conflict.feature;
            const newLit = prop.conflict.newLit;
            const oldLit = lit(f, 1 - (newLit & 1));
            const chainNew = renderChain(newLit, prop.parent, names);
            const chainOld = renderChain(oldLit, prop.parent, names);
            const used: Constraint[] = [];
            for (const L of [newLit, oldLit]) {
              let cur: number | null = L;
              while (cur !== null) {
                const p: Parent | null = prop.parent[cur] ?? null;
                if (p) {
                  if (!used.includes(p.constraint)) used.push(p.constraint);
                  cur = p.from;
                } else {
                  cur = null;
                }
              }
            }
            out.push({
              i, j, vi, vj, kind: "propagation", constraintsUsed: used,
              explanation:
                `约束传播矛盾：\n      链 1：${chainNew.join(" ⇒ ")}\n` +
                `      链 2：${chainOld.join(" ⇒ ")}\n` +
                `      特性 ${names[f]} 被同时要求取 ${newLit & 1} 和 ${1 - (newLit & 1)}`,
            });
          } else {
            // Defensive fallback: cannot happen for Horn constraints
            // (mutex/implies), where unit propagation is complete.
            const subset = minimalSubset(n, i, j, vi, vj, constraints);
            out.push({
              i, j, vi, vj, kind: "exhaustive", constraintsUsed: subset,
              explanation:
                `在约束子集 { ${subset.map((c) => formatConstraint(c, names)).join("; ")} } 下` +
                `穷举全部 2^${n} 种赋值，无任何合法配置取该组合`,
            });
          }
        }
      }
    }
  }
  return out;
}
