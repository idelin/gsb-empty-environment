/**
 * Per-kind merge algebras.
 *
 *  - limit:        stricter value wins, honouring dependency relations
 *                  (solved as difference constraints, with provenance);
 *  - mask:         union of masked fields (order-independent);
 *  - auditTags:    union of tags (order-independent, commutativity checked);
 *  - orderedSteps: order-sensitive topological merge, explicit ordering;
 *  - outputFormat: intersection of allowed choices - disjoint choices are
 *                  an explicit failure, never a silent pick.
 *
 * Any incompatibility yields a typed conflict carrying a subset-minimal
 * unsatisfiable core of the responsible obligations.
 */

import type {
  LimitObligation,
  Obligation,
  OrderedStepsObligation,
  OutputFormatObligation,
} from './obligations.ts';
import { constraintsOf, solveConstraints, type BoundEvidence } from './limits.ts';
import { minimalCore } from './core.ts';
import { deepEqual } from './util.ts';

export type MergedLimit = {
  /** strictest feasible value in the direction the limit was declared */
  value: number;
  upperBound?: number;
  lowerBound?: number;
  evidence: BoundEvidence[];
};

export type MergedObligations = {
  limits: Record<string, MergedLimit>;
  maskFields: { fields: string[]; evidence: Record<string, string[]> };
  auditTags: { tags: string[]; evidence: Record<string, string[]> };
  orderedSteps: { steps: string[]; evidence: Record<string, string[]> };
  outputFormat: { allowed: string[]; evidence: Record<string, string[]> };
};

export type Conflict = {
  kind: 'limit' | 'orderedSteps' | 'outputFormat';
  message: string;
  /** subset-minimal unsatisfiable core of obligations */
  core: Obligation[];
};

export type MergeOutcome =
  | { ok: true; merged: MergedObligations }
  | { ok: false; conflict: Conflict };

function unionWithEvidence(items: readonly { id: string; values: readonly string[] }[]): {
  values: string[];
  evidence: Record<string, string[]>;
} {
  const evidence: Record<string, string[]> = {};
  for (const item of items) {
    for (const v of item.values) {
      (evidence[v] ??= []).push(item.id);
    }
  }
  // Canonical form: sorted values and sorted source ids, so the merged
  // result is independent of input order (commutative by construction).
  const values = Object.keys(evidence).sort();
  for (const v of values) evidence[v].sort();
  return { values, evidence };
}

function mergeLimits(obs: readonly LimitObligation[]): { ok: true; limits: Record<string, MergedLimit> } | { ok: false; conflict: Conflict } {
  if (obs.length === 0) return { ok: true, limits: {} };
  const solved = solveConstraints(obs.flatMap(constraintsOf));
  if (!solved.ok) {
    const core = minimalCore(obs, (subset) => !solveConstraints(subset.flatMap(constraintsOf)).ok);
    return {
      ok: false,
      conflict: {
        kind: 'limit',
        message: `unsatisfiable limit obligations: ${core.map((o) => o.id).join(', ')}`,
        core,
      },
    };
  }
  const names = new Set<string>();
  for (const o of obs) {
    names.add(o.name);
    for (const r of o.requires ?? []) {
      names.add(r.left);
      names.add(r.right);
    }
  }
  const limits: Record<string, MergedLimit> = {};
  for (const name of names) {
    const up = solved.upper.get(name);
    const lo = solved.lower.get(name);
    const hasUpper = obs.some((o) => o.name === name && o.bound === 'upper');
    const hasLower = obs.some((o) => o.name === name && o.bound === 'lower');
    const chosen = hasUpper ? up : hasLower ? lo : up ?? lo;
    if (!chosen) continue;
    limits[name] = {
      value: chosen.value,
      ...(up ? { upperBound: up.value } : {}),
      ...(lo ? { lowerBound: lo.value } : {}),
      evidence: chosen.evidence,
    };
  }
  return { ok: true, limits };
}

function mergeOrderedSteps(
  obs: readonly OrderedStepsObligation[],
): { ok: true; steps: string[]; evidence: Record<string, string[]> } | { ok: false; conflict: Conflict } {
  const hasCycle = (subset: readonly OrderedStepsObligation[]): boolean => {
    const adj = new Map<string, Set<string>>();
    for (const o of subset) {
      for (let i = 0; i + 1 < o.steps.length; i++) {
        if (!adj.has(o.steps[i])) adj.set(o.steps[i], new Set());
        adj.get(o.steps[i])!.add(o.steps[i + 1]);
      }
    }
    const indeg = new Map<string, number>();
    for (const [a, bs] of adj) {
      if (!indeg.has(a)) indeg.set(a, 0);
      for (const b of bs) indeg.set(b, (indeg.get(b) ?? 0) + 1);
    }
    const queue = [...indeg].filter(([, d]) => d === 0).map(([n]) => n);
    let seen = 0;
    while (queue.length > 0) {
      const n = queue.shift()!;
      seen++;
      for (const b of adj.get(n) ?? []) {
        const d = indeg.get(b)! - 1;
        indeg.set(b, d);
        if (d === 0) queue.push(b);
      }
    }
    return seen < indeg.size;
  };

  if (obs.length === 0) return { ok: true, steps: [], evidence: {} };
  if (hasCycle(obs)) {
    const core = minimalCore(obs, hasCycle);
    return {
      ok: false,
      conflict: {
        kind: 'orderedSteps',
        message: `cyclic step ordering required by: ${core.map((o) => o.id).join(', ')}`,
        core,
      },
    };
  }
  // Kahn's algorithm; ties broken by first appearance across obligations in
  // input order, so each obligation's internal order is preserved explicitly.
  const firstSeen = new Map<string, number>();
  const evidence: Record<string, string[]> = {};
  const adj = new Map<string, Set<string>>();
  const indeg = new Map<string, number>();
  let counter = 0;
  for (const o of obs) {
    for (const s of o.steps) {
      if (!firstSeen.has(s)) firstSeen.set(s, counter++);
      (evidence[s] ??= []).push(o.id);
      if (!indeg.has(s)) indeg.set(s, 0);
    }
    for (let i = 0; i + 1 < o.steps.length; i++) {
      const a = o.steps[i];
      const b = o.steps[i + 1];
      if (a === b) continue;
      if (!adj.has(a)) adj.set(a, new Set());
      if (!adj.get(a)!.has(b)) {
        adj.get(a)!.add(b);
        indeg.set(b, indeg.get(b)! + 1);
      }
    }
  }
  const ready = [...indeg].filter(([, d]) => d === 0).map(([n]) => n);
  ready.sort((x, y) => firstSeen.get(x)! - firstSeen.get(y)!);
  const steps: string[] = [];
  while (ready.length > 0) {
    const n = ready.shift()!;
    steps.push(n);
    for (const b of adj.get(n) ?? []) {
      const d = indeg.get(b)! - 1;
      indeg.set(b, d);
      if (d === 0) {
        ready.push(b);
        ready.sort((x, y) => firstSeen.get(x)! - firstSeen.get(y)!);
      }
    }
  }
  return { ok: true, steps, evidence };
}

function mergeOutputFormats(
  obs: readonly OutputFormatObligation[],
): { ok: true; allowed: string[]; evidence: Record<string, string[]> } | { ok: false; conflict: Conflict } {
  if (obs.length === 0) return { ok: true, allowed: [], evidence: {} };
  const intersects = (subset: readonly OutputFormatObligation[]): string[] => {
    let acc: string[] | null = null;
    for (const o of subset) {
      acc = acc === null ? [...o.allowed] : acc.filter((f) => o.allowed.includes(f));
    }
    return acc ?? [];
  };
  const allowed = intersects(obs).sort();
  if (allowed.length === 0) {
    // Two mutually exclusive format requirements must fail loudly here.
    const core = minimalCore(obs, (subset) => subset.length > 0 && intersects(subset).length === 0);
    return {
      ok: false,
      conflict: {
        kind: 'outputFormat',
        message: `mutually exclusive output formats required by: ${core.map((o) => o.id).join(', ')}`,
        core,
      },
    };
  }
  const evidence: Record<string, string[]> = {};
  for (const f of allowed) {
    evidence[f] = obs.filter((o) => o.allowed.includes(f)).map((o) => o.id).sort();
  }
  return { ok: true, allowed, evidence };
}

export function mergeObligations(obs: readonly Obligation[]): MergeOutcome {
  const limitObs = obs.filter((o): o is LimitObligation => o.kind === 'limit');
  const maskObs = obs.filter((o) => o.kind === 'mask');
  const tagObs = obs.filter((o) => o.kind === 'auditTags');
  const stepObs = obs.filter((o): o is OrderedStepsObligation => o.kind === 'orderedSteps');
  const fmtObs = obs.filter((o): o is OutputFormatObligation => o.kind === 'outputFormat');

  const limits = mergeLimits(limitObs);
  if (!limits.ok) return { ok: false, conflict: limits.conflict };

  const mask = unionWithEvidence(maskObs.map((o) => ({ id: o.id, values: o.fields })));
  const tags = unionWithEvidence(tagObs.map((o) => ({ id: o.id, values: o.tags })));

  const steps = mergeOrderedSteps(stepObs);
  if (!steps.ok) return { ok: false, conflict: steps.conflict };

  const formats = mergeOutputFormats(fmtObs);
  if (!formats.ok) return { ok: false, conflict: formats.conflict };

  return {
    ok: true,
    merged: {
      limits: limits.limits,
      maskFields: { fields: mask.values, evidence: mask.evidence },
      auditTags: { tags: tags.values, evidence: tags.evidence },
      orderedSteps: { steps: steps.steps, evidence: steps.evidence },
      outputFormat: { allowed: formats.allowed, evidence: formats.evidence },
    },
  };
}

const ORDER_INDEPENDENT = new Set(['limit', 'mask', 'auditTags', 'outputFormat']);

/**
 * Commutativity check for order-independent kinds: for every pair of
 * obligations, merging [a, b] must equal merging [b, a]. Order-sensitive
 * kinds (orderedSteps) are rejected - their ordering is explicit instead.
 */
export function isCommutative(obs: readonly Obligation[]): boolean {
  for (const o of obs) {
    if (!ORDER_INDEPENDENT.has(o.kind)) {
      throw new Error(`kind "${o.kind}" is order-sensitive; commutativity does not apply`);
    }
  }
  for (let i = 0; i < obs.length; i++) {
    for (let j = i + 1; j < obs.length; j++) {
      const ab = mergeObligations([obs[i], obs[j]]);
      const ba = mergeObligations([obs[j], obs[i]]);
      if (!deepEqual(ab, ba)) return false;
    }
  }
  return true;
}
