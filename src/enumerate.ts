import { isJSONObject, type JSONValue } from './json.ts';
import { PlanError } from './errors.ts';
import { compilePlan } from './infer.ts';
import { runStep } from './interpret.ts';
import type { Step } from './plan.ts';
import { describe, matches, typeOf, type Type } from './types.ts';

export interface EnumOptions {
  /** 枚举输入总数上限（默认 128）。 */
  maxInputs?: number;
  /** 嵌套/数组展开深度上限（默认 2）。 */
  depth?: number;
}

const ABSENT: unique symbol = Symbol('absent');

/** 按类型有界枚举样本值：对象键无序、数组有序，深度与总数均有界。 */
function sampleValues(t: Type, depth: number, cap: number): JSONValue[] {
  switch (t.kind) {
    case 'never':
      return [];
    case 'unknown':
      return [null, 0, 's', true];
    case 'null':
      return [null];
    case 'boolean':
      return [true, false];
    case 'number':
      return [0, 1];
    case 'string':
      return ['', 'a'];
    case 'literal':
      return [t.value];
    case 'array': {
      if (depth <= 0) return [[]];
      const inner = sampleValues(t.items, depth - 1, cap);
      return inner.length === 0 ? [[]] : [[], [inner[0]!]];
    }
    case 'union':
      return t.options.flatMap((o) => sampleValues(o, depth, cap).slice(0, 1));
    case 'object': {
      if (depth <= 0) return [{}];
      let combos: JSONValue[] = [{}];
      const names = Object.keys(t.fields).sort();
      for (const name of names) {
        const f = t.fields[name]!;
        const vals = sampleValues(f.type, depth - 1, cap);
        const choices: (JSONValue | typeof ABSENT)[] = f.optional ? [ABSENT, ...vals] : vals;
        const next: JSONValue[] = [];
        for (const c of combos) {
          for (const ch of choices) {
            const o = { ...(c as Record<string, JSONValue>) };
            if (ch !== ABSENT) o[name] = ch;
            next.push(o);
            if (next.length >= cap) break;
          }
          if (next.length >= cap) break;
        }
        combos = next;
      }
      return combos;
    }
  }
}

/** 枚举满足输入模式的有限输入集合（确定性顺序，受 maxInputs 限制）。 */
export function enumerateInputs(schema: Type, opts: EnumOptions = {}): JSONValue[] {
  const max = opts.maxInputs ?? 128;
  const depth = opts.depth ?? 2;
  return sampleValues(schema, depth, max).slice(0, max);
}

export interface Counterexample {
  input: JSONValue;
  stepId: string;
  path: string;
  expected: string;
  actual: string;
}

export interface CheckResult {
  counterexamples: Counterexample[];
  checkedInputs: number;
  /** 静态编译是否干净（不干净时不存在“声称安全”的前提）。 */
  staticallyClean: boolean;
}

function checkClaims(
  value: JSONValue,
  type: Type,
  path: string,
  stepId: string,
  input: JSONValue,
  out: Counterexample[],
): void {
  if (out.length >= 20) return;
  if (type.kind === 'object') {
    if (!isJSONObject(value)) {
      out.push({ input, stepId, path, expected: describe(type), actual: describe(typeOf(value)) });
      return;
    }
    for (const [k, f] of Object.entries(type.fields)) {
      const childPath = `${path}.${k}`;
      if (k in value) {
        checkClaims(value[k]!, f.type, childPath, stepId, input, out);
      } else if (!f.optional) {
        out.push({ input, stepId, path: childPath, expected: '字段存在（静态声称必需）', actual: '缺失' });
      }
    }
    return;
  }
  if (!matches(value, type)) {
    out.push({ input, stepId, path, expected: describe(type), actual: describe(typeOf(value)) });
  }
}

/**
 * 有限枚举检查“静态声称安全但运行缺字段”的反例：
 * 对每个枚举输入逐步运行计划，核对每个顶层步骤输出是否满足推导声称
 * （必需字段存在且类型匹配）；运行抛错而静态无 error 也记为反例。
 * 可通过 claimsOverride 注入外部声称（用于审计第三方/被篡改的证书）。
 */
export function findCounterexamples(
  steps: Step[],
  inputSchema: Type,
  opts: EnumOptions & { claimsOverride?: Map<string, Type> } = {},
): CheckResult {
  const analysis = compilePlan(steps, inputSchema);
  const staticallyClean = analysis.ok;
  const claims = new Map<string, Type>();
  for (const s of steps) {
    const d = analysis.derivations.find((x) => x.stepId === s.id);
    if (d) claims.set(s.id, d.output);
  }
  if (opts.claimsOverride) {
    for (const [k, v] of opts.claimsOverride) claims.set(k, v);
  }
  const inputs = enumerateInputs(inputSchema, opts);
  const counterexamples: Counterexample[] = [];
  for (const input of inputs) {
    let cur = input;
    for (const s of steps) {
      try {
        cur = runStep(s, cur);
      } catch (e) {
        if (e instanceof PlanError) {
          if (staticallyClean) {
            counterexamples.push({
              input,
              stepId: e.stepId,
              path: e.path ?? '$',
              expected: '运行成功（静态声称安全）',
              actual: `运行时错误: ${e.message}`,
            });
          }
        } else {
          throw e;
        }
        break;
      }
      const claim = claims.get(s.id);
      if (claim && staticallyClean) {
        checkClaims(cur, claim, '$', s.id, input, counterexamples);
      }
      if (counterexamples.length >= 20) break;
    }
    if (counterexamples.length >= 20) break;
  }
  return { counterexamples, checkedInputs: inputs.length, staticallyClean };
}
