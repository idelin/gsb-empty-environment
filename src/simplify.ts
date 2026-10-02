/**
 * Composition simplification. Only adjacent affine ops are composed:
 *
 *   (a2 * (a1 * x + b1) + b2)  ==  (a1*a2) * x + (a2*b1 + b2)
 *
 * Rounding (quantize) steps are never moved, merged, dropped, or
 * reordered: rounding is not affine, so composing across it would change
 * semantics. Identity affines (scale 1, offset 0) are dropped only when
 * they arise from composition or were present as no-ops between affines.
 */
import {
  rat, add, mul, toString as ratToString,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";
import {
  affine, describeOp,
} from "./ops.ts";
import type {
  Op, AffineOp,
} from "./ops.ts";

export interface SimplifyResult {
  ops: Op[];
  log: string[];
}

function isIdentity(op: AffineOp): boolean {
  return op.scale.n === 1n && op.scale.d === 1n && op.offset.n === 0n;
}

/** Compose f then g: g(f(x)) as a single affine. */
function compose(f: AffineOp, g: AffineOp): AffineOp {
  return affine(
    mul(f.scale, g.scale),
    add(mul(g.scale, f.offset), g.offset),
  );
}

export function simplifyOps(ops: Op[]): SimplifyResult {
  const out: Op[] = [];
  const log: string[] = [];
  let pending: AffineOp | null = null;
  let pendingFrom = -1;

  const flush = (uptoIndex: number) => {
    if (pending === null) return;
    if (isIdentity(pending)) {
      log.push(`op#${pendingFrom}..op#${uptoIndex} 复合为恒等仿射，已删除（不舍及任何舍入步骤）`);
    } else {
      if (uptoIndex > pendingFrom)
        log.push(
          `合并相邻仿射 op#${pendingFrom}..op#${uptoIndex} → ${describeOp(pending)}`,
        );
      out.push(pending);
    }
    pending = null;
  };

  for (let i = 0; i < ops.length; i++) {
    const op = ops[i];
    if (op.kind === "affine") {
      if (pending === null) {
        pending = op;
        pendingFrom = i;
      } else {
        pending = compose(pending, op);
      }
    } else {
      flush(i - 1);
      out.push(op); // rounding steps pass through untouched, in order
      log.push(`op#${i} 为舍入步骤（quantize），保持原位不移动`);
    }
  }
  flush(ops.length - 1);

  const quantizeCount = (list: Op[]) => list.filter((o) => o.kind === "quantize").length;
  log.push(
    `舍入步骤数量 ${quantizeCount(ops)} → ${quantizeCount(out)}，顺序不变；仅复合相邻仿射`,
  );
  return { ops: out, log };
}

/** Forward-equivalence check of original vs simplified on given inputs. */
export function checkSimplified(
  original: Op[],
  simplified: Op[],
  inputs: Rat[],
  apply: (ops: Op[], x: Rat) => Rat,
): string[] {
  const failures: string[] = [];
  for (const x of inputs) {
    const a = apply(original, x);
    const b = apply(simplified, x);
    if (a.n !== b.n || a.d !== b.d)
      failures.push(`x=${ratToString(x)}: ${ratToString(a)} != ${ratToString(b)}`);
  }
  return failures;
}
