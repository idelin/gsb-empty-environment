/**
 * Small-scale exhaustive verification on an integer grid. For every
 * integer input x in [lo, hi]:
 *
 *  1. Forward/inverse consistency: the pipeline preimage of the point
 *     interval {forward(x)} contains exactly the grid inputs that map to
 *     forward(x) — both directions are checked.
 *  2. Error bound: |stepwise(x) - exact(x)| <= cumulative errorBound.
 *  3. Negative-zero policy: any zero output is the normalized 0/1.
 *
 * It also searches the minimal-difference input between stepwise and
 * deferred rounding (smallest |x|, ties broken by smaller x).
 */
import {
  rat, abs, sub, cmp, toString as ratToString,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";
import {
  applyOp, pipelinePreimage,
} from "./ops.ts";
import type {
  Op,
} from "./ops.ts";
import {
  point, contains, intervalToString,
} from "./interval.ts";
import {
  runPipeline, compareModes,
} from "./pipeline.ts";
import type {
  QuantizeSpec,
} from "./pipeline.ts";

export interface MinDiff {
  input: Rat;
  stepwise: Rat;
  deferred: Rat;
  reason: string;
}

export interface VerifyReport {
  lo: bigint;
  hi: bigint;
  checked: number;
  failures: string[];
  errorBound: Rat;
  maxObservedDeviation: Rat;
  minDiff: MinDiff | null;
}

export function forward(ops: Op[], x: Rat): Rat {
  let v = x;
  for (const op of ops) v = applyOp(op, v);
  return v;
}

export function exhaustiveVerify(
  ops: Op[],
  lo: bigint,
  hi: bigint,
  finalQuant: QuantizeSpec,
): VerifyReport {
  const failures: string[] = [];
  const grid: Rat[] = [];
  let bound = rat(0n);
  let maxObserved = rat(0n);
  let minDiff: MinDiff | null = null;

  for (let i = lo; i <= hi; i++) {
    const x = rat(i);
    grid.push(x);
    const sw = runPipeline(ops, x, "stepwise");
    const df = runPipeline(ops, x, "deferred", finalQuant);
    bound = sw.errorBound;

    // (2) cumulative error bound covers the real loss
    const deviation = abs(sub(sw.output, df.exactValue));
    if (cmp(deviation, sw.errorBound) > 0)
      failures.push(
        `x=${i}: 实际偏差 ${ratToString(deviation)} 超过累计误差界 ${ratToString(sw.errorBound)}`,
      );
    if (cmp(deviation, maxObserved) > 0) maxObserved = deviation;

    // (3) negative-zero policy: zero outputs are normalized 0/1
    for (const s of [...sw.steps, ...df.steps]) {
      if (s.output.n === 0n && s.output.d !== 1n)
        failures.push(`x=${i}: 零值未规范化（出现负零风险）`);
    }

    // minimal-difference input between the two modes
    if (cmp(sw.output, df.output) !== 0) {
      const better =
        minDiff === null ||
        cmp(abs(x), abs(minDiff.input)) < 0 ||
        (cmp(abs(x), abs(minDiff.input)) === 0 && cmp(x, minDiff.input) < 0);
      if (better) {
        minDiff = {
          input: x,
          stepwise: sw.output,
          deferred: df.output,
          reason: compareModes(ops, x, finalQuant).reason,
        };
      }
    }
  }

  // (1) forward/inverse consistency over the grid
  for (const x of grid) {
    const y = forward(ops, x);
    const pre = pipelinePreimage(ops, point(y));
    if (!pre.some((iv) => contains(iv, x)))
      failures.push(
        `x=${ratToString(x)}: 不在输出 ${ratToString(y)} 的原像 ${pre.map(intervalToString).join(" ∪ ")} 中`,
      );
    for (const x2 of grid) {
      const inPre = pre.some((iv) => contains(iv, x2));
      const mapsToY = eqRat(forward(ops, x2), y);
      if (inPre !== mapsToY)
        failures.push(
          `原像不一致: x'=${ratToString(x2)} 在原像中=${inPre}，但 forward(x')==${ratToString(y)} 为 ${mapsToY}`,
        );
    }
  }

  return {
    lo, hi,
    checked: grid.length,
    failures,
    errorBound: bound,
    maxObservedDeviation: maxObserved,
    minDiff,
  };
}

function eqRat(a: Rat, b: Rat): boolean {
  return a.n === b.n && a.d === b.d;
}
