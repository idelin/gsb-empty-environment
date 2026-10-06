/**
 * Top-level analysis API: exhaustiveness, unreachable (fully covered)
 * branches, minimal-depth witnesses, and depth-bounded enumeration
 * cross-validation.
 */

import { Ctx } from "./ctx";
import { enumerateValues } from "./enumerate";
import { Matrix, PatternVector, useful, vectorDepth } from "./matrix";
import { matchPattern, Pattern, showPattern, wild } from "./pattern";
import { showValue, TypeEnv, Value, valueDepth, valueEq } from "./types";

export interface BranchReport {
  index: number;
  /** "unreachable" means the branch is fully covered by the branches above it. */
  status: "reachable" | "unreachable";
  /** A minimal-depth value vector whose first matching branch is this one. */
  witness?: Value[];
  witnessDepth?: number;
}

export interface CrossValidation {
  depthBound: number;
  enumeratedValues: number;
  truncated: boolean;
  /** Branches never first-matched by any enumerated value up to the bound. */
  unreachableUpToDepth: number[];
  /** Enumerated value vectors matched by no branch (only if not exhaustive). */
  unmatchedExamples: Value[][];
  /** False iff any enumeration result contradicts the symbolic algorithm. */
  consistent: boolean;
  findings: string[];
}

export interface Report {
  exhaustive: boolean;
  /** Minimal constructor-depth counterexample when not exhaustive. */
  counterexample?: Value[];
  counterexampleDepth?: number;
  branches: BranchReport[];
  crossValidation?: CrossValidation;
}

export interface AnalyzeOptions {
  /** Depth bound for the enumeration cross-check (default 6; <=0 disables). */
  enumDepth?: number;
  /** Cap on enumerated values per type (default 100000). */
  enumCap?: number;
}

function validatePattern(ctx: Ctx, p: Pattern, t: string): void {
  if (p.kind !== "ctor") return;
  const info = ctx.ctorInfo(p.ctor);
  if (info.typeName !== t) {
    throw new Error(
      `constructor "${p.ctor}" belongs to type "${info.typeName}", not "${t}"`
    );
  }
  if (p.args.length !== info.def.fields.length) {
    throw new Error(
      `constructor "${p.ctor}" expects ${info.def.fields.length} argument(s), got ${p.args.length}`
    );
  }
  p.args.forEach((sub, i) => validatePattern(ctx, sub, info.def.fields[i]!));
}

function materialize(ctx: Ctx, pats: PatternVector, ts: string[]): Value[] {
  return pats.map((p, i) => materializePat(ctx, p, ts[i]!));
}

function materializePat(ctx: Ctx, p: Pattern, t: string): Value {
  if (p.kind !== "ctor") return ctx.minValue(t);
  const info = ctx.ctorInfo(p.ctor);
  return {
    ctor: p.ctor,
    args: p.args.map((sub, i) => materializePat(ctx, sub, info.def.fields[i]!)),
  };
}

export function analyze(
  env: TypeEnv,
  scrutineeTypes: string[],
  rows: Pattern[][],
  opts: AnalyzeOptions = {}
): Report {
  const ctx = new Ctx(env);
  const ts = scrutineeTypes;
  for (const t of ts) {
    if (!env[t]) throw new Error(`unknown scrutinee type "${t}"`);
  }
  rows.forEach((row, i) => {
    if (row.length !== ts.length) {
      throw new Error(`branch ${i} has ${row.length} pattern(s), expected ${ts.length}`);
    }
    row.forEach((p, j) => validatePattern(ctx, p, ts[j]!));
  });

  const P: Matrix = rows.map((r) => [...r]);

  const missingWitness = useful(ctx, P, ts.map(() => wild()), ts);
  const exhaustive = missingWitness === null;
  const report: Report = { exhaustive, branches: [] };
  if (missingWitness) {
    report.counterexample = materialize(ctx, missingWitness, ts);
    report.counterexampleDepth = vectorDepth(ctx, missingWitness, ts);
  }

  for (let i = 0; i < rows.length; i++) {
    const w = useful(ctx, P.slice(0, i), rows[i]!, ts);
    if (w) {
      report.branches.push({
        index: i,
        status: "reachable",
        witness: materialize(ctx, w, ts),
        witnessDepth: vectorDepth(ctx, w, ts),
      });
    } else {
      report.branches.push({ index: i, status: "unreachable" });
    }
  }

  const enumDepth = opts.enumDepth === undefined ? 6 : opts.enumDepth;
  if (enumDepth > 0) {
    report.crossValidation = crossValidate(
      ctx,
      ts,
      rows,
      report,
      enumDepth,
      opts.enumCap ?? 100_000
    );
  }
  return report;
}

function crossValidate(
  ctx: Ctx,
  ts: string[],
  rows: Pattern[][],
  report: Report,
  depthBound: number,
  cap: number
): CrossValidation {
  const findings: string[] = [];
  let consistent = true;
  const fail = (msg: string) => {
    consistent = false;
    findings.push(`MISMATCH: ${msg}`);
  };

  const en = enumerateValues(ctx, ts, depthBound, cap);
  const firstMatch = new Map<number, number>();
  const unmatched: Value[][] = [];
  let unmatchedCount = 0;
  let minUnmatchedDepth = Infinity;
  let vectorCount = 0;
  const vectorCap = cap;

  const columns = ts.map((t) => en.byType.get(t)!);
  const idx = new Array<number>(ts.length).fill(0);
  const done0 = ts.length === 0 || columns.some((c) => c.length === 0);
  if (done0 && ts.some((t) => ctx.inhabited(t)) && !en.truncated) {
    findings.push(
      `note: depth bound ${depthBound} too small to enumerate some inhabited type`
    );
  }
  let done = done0;
  while (!done) {
    if (vectorCount >= vectorCap) {
      findings.push(`note: value-vector enumeration capped at ${vectorCap}`);
      break;
    }
    vectorCount++;
    const vec = idx.map((k, i) => columns[i]![k]!);
    let hit = -1;
    for (let i = 0; i < rows.length; i++) {
      if (rows[i]!.every((p, j) => matchPattern(p, vec[j]!))) {
        hit = i;
        break;
      }
    }
    if (hit === -1) {
      unmatchedCount++;
      const d = Math.max(...vec.map(valueDepth));
      minUnmatchedDepth = Math.min(minUnmatchedDepth, d);
      if (unmatched.length < 5) unmatched.push(vec);
    } else {
      firstMatch.set(hit, (firstMatch.get(hit) ?? 0) + 1);
    }
    let pos = idx.length - 1;
    while (pos >= 0 && ++idx[pos]! === columns[pos]!.length) {
      idx[pos] = 0;
      pos--;
    }
    if (pos < 0) done = true;
  }

  if (report.exhaustive) {
    if (unmatchedCount > 0) {
      fail(`symbolic says exhaustive but ${unmatchedCount} enumerated value(s) match nothing`);
    } else if (!en.truncated) {
      findings.push(`all ${vectorCount} enumerated value vector(s) are matched`);
    }
  } else {
    const d = report.counterexampleDepth!;
    if (minUnmatchedDepth !== Infinity && minUnmatchedDepth < d) {
      fail(
        `found unmatched value of depth ${minUnmatchedDepth} < symbolic minimal witness depth ${d}`
      );
    }
    if (d <= depthBound) {
      const ce = report.counterexample!;
      let first = -1;
      for (let i = 0; i < rows.length; i++) {
        if (rows[i]!.every((p, j) => matchPattern(p, ce[j]!))) { first = i; break; }
      }
      if (first !== -1) fail(`symbolic counterexample is matched by branch ${first}`);
    }
    if (minUnmatchedDepth !== Infinity && minUnmatchedDepth === d && !en.truncated) {
      findings.push(`minimal unmatched enumerated depth ${d} agrees with symbolic witness`);
    }
  }

  const unreachableUpToDepth: number[] = [];
  for (const b of report.branches) {
    const hits = firstMatch.get(b.index) ?? 0;
    if (b.status === "unreachable") {
      unreachableUpToDepth.push(b.index);
      if (hits > 0) {
        fail(`branch ${b.index} is symbolically unreachable but first-matched ${hits} enumerated value(s)`);
      }
    } else {
      if (hits === 0) unreachableUpToDepth.push(b.index);
      const wd = b.witnessDepth!;
      if (hits === 0 && wd <= depthBound && !en.truncated) {
        fail(`branch ${b.index} is symbolically reachable (witness depth ${wd}) but never first-matched up to depth ${depthBound}`);
      }
    }
  }
  if (consistent) {
    findings.push("enumeration agrees with the symbolic algorithm on all compared facts");
  }

  return {
    depthBound,
    enumeratedValues: en.total,
    truncated: en.truncated,
    unreachableUpToDepth,
    unmatchedExamples: unmatched,
    consistent,
    findings,
  };
}

/** Convenience wrapper for the common single-scrutinee case. */
export function analyzeMatch(
  env: TypeEnv,
  scrutineeType: string,
  branches: Pattern[],
  opts: AnalyzeOptions = {}
): Report {
  return analyze(env, [scrutineeType], branches.map((b) => [b]), opts);
}

export function showVector(vals: Value[]): string {
  return vals.map(showValue).join(", ");
}

export function showPatternVector(pats: Pattern[]): string {
  return pats.map(showPattern).join(", ");
}
