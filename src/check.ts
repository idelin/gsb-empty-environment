import type { Formula } from "./formula.ts";
import { existsDisjointPair } from "./search.ts";
import type { SearchStats } from "./search.ts";
import { lexMinWitness } from "./witness.ts";
import type { AggregateStats } from "./witness.ts";

export const MAX_NODES = 20;

export interface ProofResult {
  intersects: true;
  proof: SearchStats & { method: string };
}

export interface WitnessResult {
  intersects: false;
  witness: { a: number[]; b: number[] };
  search: AggregateStats;
}

export type CheckResult = ProofResult | WitnessResult;

export function checkIntersection(fa: Formula, fb: Formula, n: number): CheckResult {
  if (!Number.isInteger(n) || n < 1 || n > MAX_NODES) {
    throw new Error(`节点数必须是 [1, ${MAX_NODES}] 内的整数，收到 ${n}`);
  }
  const r = existsDisjointPair(fa, fb, n);
  if (!r.found) {
    return {
      intersects: true,
      proof: { ...r.stats, method: "branch-and-bound+memoization" },
    };
  }
  const w = lexMinWitness(fa, fb, n);
  if (w === null) {
    throw new Error("内部错误：存在性搜索与见证构造不一致");
  }
  return { intersects: false, witness: { a: w.a, b: w.b }, search: w.stats };
}
