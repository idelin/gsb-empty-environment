// ---------------------------------------------------------------------------
// 随机模糊测试：用可复现的种子随机生成表达式，对每个表达式做穷举小环境
// 验证（值 / 首个错误 / 变量访问序列必须与原表达式一致）。
// ---------------------------------------------------------------------------

import { Builder, type Node, type Value } from './ast.ts';
import { enumerateEnvs, outcomeEqual, DOMAIN } from './verify.ts';
import { evaluate } from './eval.ts';
import { fold } from './fold.ts';
import { collectVars } from './ast.ts';

// mulberry32：可复现的伪随机数
function rng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const VARS = ['x', 'y', 'z'];
const CONSTS: Value[] = [0, 1, 2, 3, false, true];

function randomExpr(rand: () => number, b: Builder, depth: number): Node {
  const pick = <T>(arr: T[]): T => arr[Math.floor(rand() * arr.length)];
  if (depth <= 0) {
    return rand() < 0.5 ? b.const(pick(CONSTS)) : b.var(pick(VARS));
  }
  const kind = Math.floor(rand() * 6);
  switch (kind) {
    case 0: return b.const(pick(CONSTS));
    case 1: return b.var(pick(VARS));
    case 2: return b.bin(pick(['+', '-', '*', '/'] as const),
      randomExpr(rand, b, depth - 1), randomExpr(rand, b, depth - 1));
    case 3: return b.logic(pick(['&&', '||'] as const),
      randomExpr(rand, b, depth - 1), randomExpr(rand, b, depth - 1));
    default: return b.cond(randomExpr(rand, b, depth - 1),
      randomExpr(rand, b, depth - 1), randomExpr(rand, b, depth - 1));
  }
}

export function fuzz(count: number, seed: number, maxDepth: number): number {
  const rand = rng(seed);
  let failures = 0;
  for (let i = 0; i < count; i++) {
    const b = new Builder();
    const original = randomExpr(rand, b, maxDepth);
    const optimized = fold(original) as Node;
    const vars = [...collectVars(original)].sort();
    for (const env of enumerateEnvs(vars, DOMAIN)) {
      const a = evaluate(original, env);
      const c = evaluate(optimized, env);
      if (!outcomeEqual(a, c)) {
        failures++;
        console.error(`FUZZ MISMATCH #${i} env=${JSON.stringify(env)}`);
        console.error(`  original : ${JSON.stringify(a)}`);
        console.error(`  optimized: ${JSON.stringify(c)}`);
        break;
      }
    }
  }
  return failures;
}
