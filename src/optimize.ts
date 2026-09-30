import { PlanError } from './errors.ts';
import { compilePlan, type StepDerivation } from './infer.ts';
import { runPlan } from './interpret.ts';
import type { Step } from './plan.ts';
import type { Type } from './types.ts';
import type { JSONValue } from './json.ts';

export interface RemovedStep {
  stepId: string;
  reason: string;
}

export interface OptimizeResult {
  steps: Step[];
  removed: RemovedStep[];
}

/**
 * 冗余步骤消除（只删不换序）：
 * 1. 删除不存在字段的 delete —— 无操作；
 * 2. 保留全部现有字段的 project —— 无操作；
 * 3. 自改名 rename a→a —— 无操作；
 * 4. 结果在被读取前即被覆盖的 compute —— 死代码。
 *
 * 错误触发顺序保持：仅当步骤自身无任何 error 诊断（可证明不会触发错误）
 * 时才允许删除；保留步骤相对顺序不变，因此首个运行时错误不变。
 */
export function optimizePlan(steps: Step[], inputSchema: Type): OptimizeResult {
  const analysis = compilePlan(steps, inputSchema);
  const derivById = new Map<string, StepDerivation>(analysis.derivations.map((d) => [d.stepId, d]));
  const removed: RemovedStep[] = [];
  const removeIds = new Set<string>();

  for (let i = 0; i < steps.length; i++) {
    const s = steps[i]!;
    const d = derivById.get(s.id);
    if (!d) continue;
    // 可能触发错误的步骤一律保留，保证错误触发顺序。
    if (d.diagnostics.some((x) => x.severity === 'error')) continue;
    const before = d.input;
    let reason: string | null = null;

    switch (s.op) {
      case 'delete':
        if (before.kind === 'object' && !(s.field in before.fields)) {
          reason = `删除不存在字段 $.${s.field} 的无操作步骤`;
        }
        break;
      case 'project':
        if (
          before.kind === 'object' &&
          Object.keys(before.fields).every((f) => s.fields.includes(f))
        ) {
          reason = `投影保留全部现有字段，无操作`;
        }
        break;
      case 'rename':
        if (s.from === s.to) reason = `自改名 $.${s.from}→$.${s.to}，无操作`;
        break;
      case 'compute': {
        const target = '$.' + s.target;
        let dead = false;
        for (let j = i + 1; j < steps.length; j++) {
          const dj = derivById.get(steps[j]!.id);
          if (!dj) break;
          if (dj.reads.includes(target)) {
            dead = false;
            break;
          }
          if (dj.writes.includes(target)) {
            dead = true;
            break;
          }
        }
        if (dead) reason = `计算结果 $.${s.target} 在被读取前被覆盖（死代码）`;
        break;
      }
      default:
        break;
    }
    if (reason) {
      removeIds.add(s.id);
      removed.push({ stepId: s.id, reason });
    }
  }
  return { steps: steps.filter((s) => !removeIds.has(s.id)), removed };
}

export interface FirstError {
  stepId: string;
  message: string;
  path?: string;
}

/** 运行计划并返回首个运行时错误（无错误返回 null）。 */
export function firstRuntimeError(steps: Step[], input: JSONValue): FirstError | null {
  try {
    runPlan(steps, input);
    return null;
  } catch (e) {
    if (e instanceof PlanError) return { stepId: e.stepId, message: e.message, path: e.path };
    throw e;
  }
}

export interface ErrorOrderCheck {
  ok: boolean;
  mismatches: { input: JSONValue; original: FirstError | null; optimized: FirstError | null }[];
}

/** 在有限输入集上验证：优化前后首个运行时错误（步骤 id）完全一致。 */
export function verifyErrorOrderPreserved(
  original: Step[],
  optimized: Step[],
  inputs: JSONValue[],
): ErrorOrderCheck {
  const mismatches: ErrorOrderCheck['mismatches'] = [];
  for (const input of inputs) {
    const a = firstRuntimeError(original, input);
    const b = firstRuntimeError(optimized, input);
    const same =
      (a === null && b === null) ||
      (a !== null && b !== null && a.stepId === b.stepId && a.path === b.path);
    if (!same) mismatches.push({ input, original: a, optimized: b });
  }
  return { ok: mismatches.length === 0, mismatches };
}
