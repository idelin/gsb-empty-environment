// ---------------------------------------------------------------------------
// 穷举验证：对小环境做笛卡尔积穷举，逐环境比较“原表达式”和“优化后表达式”
// 的可观察行为 —— 值、首个错误（种类 + 节点 ID）、变量访问序列。
// ---------------------------------------------------------------------------

import { collectVars, type Node, type Value } from './ast.ts';
import { evaluate, type Env, type Outcome, type Trace } from './eval.ts';
import { fold } from './fold.ts';

export const DOMAIN: Value[] = [0, 1, 2, false, true];

export function* enumerateEnvs(names: string[], domain: Value[] = DOMAIN): Generator<Env> {
  if (names.length === 0) { yield {}; return; }
  const [head, ...rest] = names;
  for (const v of domain) {
    for (const tail of enumerateEnvs(rest, domain)) {
      yield { [head]: v, ...tail };
    }
  }
}

function traceEqual(a: Trace, b: Trace): boolean {
  return a.length === b.length &&
    a.every((e, i) => e.kind === b[i].kind && e.id === b[i].id &&
      e.name === b[i].name && e.value === b[i].value);
}

export function outcomeEqual(a: Outcome, b: Outcome): boolean {
  if (a.ok !== b.ok) return false;
  if (!traceEqual(a.trace, b.trace)) return false;
  if (a.ok && b.ok) return a.value === b.value;
  if (!a.ok && !b.ok) return a.error.kind === b.error.kind && a.error.id === b.error.id;
  return true;
}

export interface Mismatch {
  env: Env;
  original: Outcome;
  optimized: Outcome;
}

export interface VerifyReport {
  name: string;
  source: string;
  envCount: number;
  mismatches: Mismatch[];
  optimized: Node;
}

export function verifyCase(name: string, source: string, original: Node): VerifyReport {
  const optimized = fold(original) as Node;
  const vars = [...collectVars(original)].sort();
  const mismatches: Mismatch[] = [];
  let envCount = 0;
  for (const env of enumerateEnvs(vars)) {
    envCount++;
    const a = evaluate(original, env);
    const b = evaluate(optimized, env);
    if (!outcomeEqual(a, b)) mismatches.push({ env, original: a, optimized: b });
  }
  return { name, source, envCount, mismatches, optimized };
}

export function formatOutcome(o: Outcome): string {
  const trace = o.trace.map(e => `read ${e.name}#${e.id}=${JSON.stringify(e.value)}`).join(', ');
  const head = o.ok
    ? `value ${JSON.stringify(o.value)}`
    : `error ${o.error.kind}@#${o.error.id}`;
  return `${head} | trace: [${trace}]`;
}
