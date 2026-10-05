export interface RelationSpec {
  name: string;
  cardinality: number;
}

export interface JoinPredicate {
  left: string;
  right: string;
  selectivity: number;
}

export interface MaterializationOptions {
  perTuple?: number;
  flat?: number;
}

export interface OptimizerOptions {
  allowCrossProducts?: boolean;
  materialization?: MaterializationOptions;
}

export interface QuerySpec {
  relations: RelationSpec[];
  predicates: JoinPredicate[];
  options?: OptimizerOptions;
}

export interface PlanNode {
  kind: "scan" | "join";
  relations: string[];
  cardinality: number;
  edgeSelectivity: number;
  crossProduct: boolean;
  intermediateCost: number;
  totalCost: number;
  left: PlanNode | null;
  right: PlanNode | null;
}

export interface OptimizeResult {
  plan: PlanNode | null;
  cost: number;
  subsetsEvaluated: number;
  partitionsEvaluated: number;
}

export const MAX_RELATIONS = 12;

interface DpEntry {
  cost: number;
  card: number;
  left: number;
  sel: number;
  cross: boolean;
}

interface Edge {
  mask: number;
  selectivity: number;
}

interface CompiledQuery {
  n: number;
  cardinalities: number[];
  names: string[];
  edges: Edge[];
  allowCross: boolean;
  matPerTuple: number;
  matFlat: number;
}

function compile(query: QuerySpec): CompiledQuery {
  const relations = query.relations;
  const predicates = query.predicates || [];
  const options = query.options || {};
  const n = relations.length;
  if (n === 0) {
    throw new Error("query must contain at least one relation");
  }
  if (n > MAX_RELATIONS) {
    throw new Error("supports at most " + MAX_RELATIONS + " relations, got " + n);
  }
  const index = new Map<string, number>();
  const cardinalities: number[] = [];
  const names: string[] = [];
  for (let i = 0; i < n; i++) {
    const rel = relations[i];
    if (index.has(rel.name)) {
      throw new Error("duplicate relation name: " + rel.name);
    }
    if (!(rel.cardinality > 0)) {
      throw new Error("relation " + rel.name + " has invalid cardinality " + rel.cardinality);
    }
    index.set(rel.name, i);
    cardinalities.push(rel.cardinality);
    names.push(rel.name);
  }
  const combined = new Map<string, number>();
  for (const pred of predicates) {
    const li = index.get(pred.left);
    const ri = index.get(pred.right);
    if (li === undefined || ri === undefined) {
      throw new Error("predicate references unknown relation: " + pred.left + ", " + pred.right);
    }
    if (li === ri) {
      continue;
    }
    if (!(pred.selectivity > 0 && pred.selectivity <= 1)) {
      throw new Error("predicate " + pred.left + "-" + pred.right + " has invalid selectivity " + pred.selectivity);
    }
    const key = li < ri ? li + ":" + ri : ri + ":" + li;
    combined.set(key, (combined.get(key) || 1) * pred.selectivity);
  }
  const edges: Edge[] = [];
  for (const entry of Array.from(combined.entries())) {
    const parts = entry[0].split(":");
    const bit = (1 << Number(parts[0])) | (1 << Number(parts[1]));
    edges.push({ mask: bit, selectivity: entry[1] });
  }
  const mat = options.materialization || {};
  return {
    n,
    cardinalities,
    names,
    edges,
    allowCross: options.allowCrossProducts === true,
    matPerTuple: mat.perTuple || 0,
    matFlat: mat.flat || 0,
  };
}

function solve(q: CompiledQuery, leftDeepOnly: boolean): OptimizeResult {
  const n = q.n;
  const size = 1 << n;
  const dp: (DpEntry | null)[] = new Array<DpEntry | null>(size).fill(null);
  for (let i = 0; i < n; i++) {
    dp[1 << i] = { cost: 0, card: q.cardinalities[i], left: -1, sel: 1, cross: false };
  }
  let subsetsEvaluated = 0;
  let partitionsEvaluated = 0;
  for (let mask = 1; mask < size; mask++) {
    if ((mask & (mask - 1)) === 0) {
      continue;
    }
    subsetsEvaluated++;
    const lowest = mask & -mask;
    let best: DpEntry | null = null;
    let s1 = (mask - 1) & mask;
    while (s1 > 0) {
      if ((s1 & lowest) !== 0) {
        const s2 = mask ^ s1;
        if (leftDeepOnly && (s2 & (s2 - 1)) !== 0) {
          s1 = (s1 - 1) & mask;
          continue;
        }
        const a = dp[s1];
        const b = dp[s2];
        if (a !== null && b !== null) {
          partitionsEvaluated++;
          let sel = 1;
          let connected = false;
          for (const e of q.edges) {
            if ((e.mask & s1) !== 0 && (e.mask & s2) !== 0) {
              sel *= e.selectivity;
              connected = true;
            }
          }
          if (connected || q.allowCross) {
            const card = a.card * b.card * sel;
            const nodeCost = card + q.matFlat + q.matPerTuple * card;
            const total = a.cost + b.cost + nodeCost;
            if (best === null || total < best.cost) {
              best = { cost: total, card, left: s1, sel, cross: !connected };
            }
          }
        }
      }
      s1 = (s1 - 1) & mask;
    }
    dp[mask] = best;
  }
  const full = size - 1;
  const rootEntry = dp[full];
  if (rootEntry === null) {
    return { plan: null, cost: Infinity, subsetsEvaluated, partitionsEvaluated };
  }
  const plan = buildPlan(q, dp, full);
  return { plan, cost: rootEntry.cost, subsetsEvaluated, partitionsEvaluated };
}

function bitIndex(mask: number): number {
  let i = 0;
  let m = mask;
  while ((m & 1) === 0) {
    m >>= 1;
    i++;
  }
  return i;
}

function buildPlan(q: CompiledQuery, dp: (DpEntry | null)[], mask: number): PlanNode {
  const entry = dp[mask] as DpEntry;
  if (entry.left === -1) {
    const i = bitIndex(mask);
    return {
      kind: "scan",
      relations: [q.names[i]],
      cardinality: entry.card,
      edgeSelectivity: 1,
      crossProduct: false,
      intermediateCost: 0,
      totalCost: 0,
      left: null,
      right: null,
    };
  }
  const s1 = entry.left;
  const s2 = mask ^ s1;
  const leftNode = buildPlan(q, dp, s1);
  const rightNode = buildPlan(q, dp, s2);
  const leftEntry = dp[s1] as DpEntry;
  const rightEntry = dp[s2] as DpEntry;
  return {
    kind: "join",
    relations: leftNode.relations.concat(rightNode.relations).sort(),
    cardinality: entry.card,
    edgeSelectivity: entry.sel,
    crossProduct: entry.cross,
    intermediateCost: entry.cost - leftEntry.cost - rightEntry.cost,
    totalCost: entry.cost,
    left: leftNode,
    right: rightNode,
  };
}

export function optimize(query: QuerySpec): OptimizeResult {
  return solve(compile(query), false);
}

export function optimizeLeftDeep(query: QuerySpec): OptimizeResult {
  return solve(compile(query), true);
}

export function formatNumber(x: number): string {
  if (!Number.isFinite(x)) {
    return String(x);
  }
  return String(Math.round(x * 10000) / 10000);
}

function describeNode(node: PlanNode): string {
  if (node.kind === "scan") {
    return "Scan " + node.relations[0] + "  card=" + formatNumber(node.cardinality);
  }
  const cross = node.crossProduct ? "  [cross product]" : "";
  return (
    "Join [" + node.relations.join(",") + "]" +
    "  card=" + formatNumber(node.cardinality) +
    "  sel=" + formatNumber(node.edgeSelectivity) +
    "  nodeCost=" + formatNumber(node.intermediateCost) +
    "  cumCost=" + formatNumber(node.totalCost) +
    cross
  );
}

export function formatPlan(plan: PlanNode): string {
  const lines: string[] = [];
  const walk = (node: PlanNode, prefix: string, isLast: boolean, isRoot: boolean): void => {
    const connector = isRoot ? "" : isLast ? "└─ " : "├─ ";
    lines.push(prefix + connector + describeNode(node));
    const childPrefix = prefix + (isRoot ? "" : isLast ? "   " : "│  ");
    if (node.left !== null && node.right !== null) {
      walk(node.left, childPrefix, false, false);
      walk(node.right, childPrefix, true, false);
    }
  };
  walk(plan, "", true, true);
  return lines.join("\n");
}
