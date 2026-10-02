/**
 * Pipeline runner with per-step loss auditing and two rounding modes:
 *
 *  - "stepwise": every quantize op rounds immediately (losses accumulate
 *    step by step and are amplified by later affine scales).
 *  - "deferred": quantize ops are passed through exactly and a single
 *    unified rounding is applied at the end (finalQuant spec).
 *
 * Both modes are first-class: their results may differ and neither is
 * normalized away. The cumulative error bound covers the real loss:
 * bound(stepwise output vs. never-rounded exact value) <= errorBound.
 *
 * Negative-zero policy: all values are normalized Rationals, zero is
 * always 0/1; no negative zero can appear in either mode.
 */
import {
  rat, add, mul, sub, abs, cmp, toString as ratToString,
} from "./rational.ts";
import type {
  Rat,
} from "./rational.ts";
import {
  findSegment, applyOp, opMaxLoss, opName, describeOp,
} from "./ops.ts";
import type {
  Op, QuantizeOp,
} from "./ops.ts";
import {
  quantize, maxQuantizeLoss,
} from "./rounding.ts";
import type {
  RoundingRule,
} from "./rounding.ts";

export type RunMode = "stepwise" | "deferred";

export interface QuantizeSpec {
  step: Rat;
  rule: RoundingRule;
}

export interface StepRecord {
  opIndex: number; // -1 for the deferred final rounding
  name: string;
  description: string;
  kind: "affine" | "quantize" | "final-round";
  input: Rat;
  output: Rat;
  /** |output - input| actually lost at this step (0 for affine / deferred pass-through). */
  loss: Rat;
  /** Worst-case loss this step could add (0 for affine / deferred pass-through). */
  maxLoss: Rat;
  deferred: boolean;
}

export interface RunResult {
  mode: RunMode;
  input: Rat;
  output: Rat;
  steps: StepRecord[];
  /** Exact value with no rounding at all (affine ops only). */
  exactValue: Rat;
  /** Bound on |output - exactValue| (covers the real accumulated loss). */
  errorBound: Rat;
}

export function runPipeline(
  ops: Op[],
  x: Rat,
  mode: RunMode,
  finalQuant?: QuantizeSpec,
): RunResult {
  if (mode === "deferred" && finalQuant === undefined)
    throw new Error("deferred mode requires a finalQuant spec");
  let v = x;
  let bound = rat(0n);
  const steps: StepRecord[] = [];
  for (let i = 0; i < ops.length; i++) {
    const op = ops[i];
    const name = opName(op, i);
    if (op.kind === "affine") {
      const out = applyOp(op, v);
      bound = mul(bound, abs(op.scale)); // later scales amplify earlier losses
      steps.push({
        opIndex: i, name, description: describeOp(op), kind: "affine",
        input: v, output: out, loss: rat(0n), maxLoss: rat(0n), deferred: false,
      });
      v = out;
    } else if (mode === "stepwise") {
      const seg = findSegment(op, v);
      const out = quantize(v, seg.step, seg.rule);
      const loss = abs(sub(out, v));
      const maxLoss = maxQuantizeLoss(seg.step, seg.rule);
      bound = add(bound, maxLoss);
      steps.push({
        opIndex: i, name, description: describeOp(op), kind: "quantize",
        input: v, output: out, loss, maxLoss, deferred: false,
      });
      v = out;
    } else {
      steps.push({
        opIndex: i, name, description: describeOp(op), kind: "quantize",
        input: v, output: v, loss: rat(0n), maxLoss: rat(0n), deferred: true,
      });
    }
  }
  const exactValue = v;
  if (mode === "deferred") {
    const spec = finalQuant as QuantizeSpec;
    const out = quantize(v, spec.step, spec.rule);
    const loss = abs(sub(out, v));
    bound = add(bound, maxQuantizeLoss(spec.step, spec.rule));
    steps.push({
      opIndex: -1, name: "final-round",
      description: `unified round(step=${ratToString(spec.step)}, ${spec.rule})`,
      kind: "final-round", input: v, output: out, loss,
      maxLoss: maxQuantizeLoss(spec.step, spec.rule), deferred: false,
    });
    v = out;
  }
  return { mode, input: x, output: v, steps, exactValue, errorBound: bound };
}

export interface ModeComparison {
  input: Rat;
  stepwise: RunResult;
  deferred: RunResult;
  differs: boolean;
  /** Human-readable explanation of why the two modes differ (or agree). */
  reason: string;
}

/** Run both modes on the same input and explain the difference. */
export function compareModes(ops: Op[], x: Rat, finalQuant: QuantizeSpec): ModeComparison {
  const stepwise = runPipeline(ops, x, "stepwise");
  const deferred = runPipeline(ops, x, "deferred", finalQuant);
  const differs = cmp(stepwise.output, deferred.output) !== 0;
  if (!differs) {
    return {
      input: x, stepwise, deferred, differs,
      reason: `两模式结果一致：${ratToString(stepwise.output)}（逐步中间舍入损失恰好被后续步骤吸收）`,
    };
  }
  // Locate the first stepwise quantize step that actually lost something:
  // that is where the two trajectories diverged.
  const culprit = stepwise.steps.find((s) => s.kind === "quantize" && s.loss.n !== 0n);
  const diff = abs(sub(stepwise.output, deferred.output));
  let reason: string;
  if (culprit === undefined) {
    reason = `差异 ${ratToString(diff)}：逐步模式各舍入步均无损失，差异来自末端统一舍入规则/步长与分段规则不同`;
  } else {
    // Product of |scale| of affine ops after the culprit amplifies its loss.
    let amp = rat(1n);
    for (let i = culprit.opIndex + 1; i < ops.length; i++)
      if (ops[i].kind === "affine") amp = mul(amp, abs((ops[i] as { scale: Rat }).scale));
    reason =
      `差异 ${ratToString(diff)}：首个分歧点在 ${culprit.name}（${culprit.description}），` +
      `输入 ${ratToString(culprit.input)} 被逐步舍入到 ${ratToString(culprit.output)}，` +
      `当场损失 ${ratToString(culprit.loss)}，经后续仿射放大系数 ${ratToString(amp)}；` +
      `延迟模式保留精确值 ${ratToString(deferred.exactValue)} 直到末端统一舍入`;
  }
  return { input: x, stepwise, deferred, differs, reason };
}

/** Total worst-case loss bound for the stepwise mode (no input needed). */
export function pipelineErrorBound(ops: Op[]): Rat {
  let bound = rat(0n);
  for (const op of ops) {
    if (op.kind === "affine") bound = mul(bound, abs(op.scale));
    else bound = add(bound, opMaxLoss(op as QuantizeOp));
  }
  return bound;
}
