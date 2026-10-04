// ---------------------------------------------------------------------------
// 求值器：严格从左到右求值；&&/|| 与 ?: 短路，未执行分支不产生任何效果。
// 可观察行为 = 变量读取序列（含节点 ID）+ 首个错误（含出错节点 ID）。
// ---------------------------------------------------------------------------

import { computeBin, truthy, type Node, type Value } from './ast.ts';

export type Env = Record<string, Value>;

export interface VarRead { kind: 'var'; id: number; name: string; value: Value }
export type Trace = VarRead[];

export interface DivByZero { kind: 'DivByZero'; id: number }

export type Outcome =
  | { ok: true; value: Value; trace: Trace }
  | { ok: false; error: DivByZero; trace: Trace };

export function evaluate(node: Node, env: Env): Outcome {
  const trace: Trace = [];
  try {
    return { ok: true, value: run(node, env, trace), trace };
  } catch (e) {
    return { ok: false, error: e as DivByZero, trace };
  }
}

function run(node: Node, env: Env, trace: Trace): Value {
  switch (node.kind) {
    case 'const':
      return node.value;
    case 'var': {
      if (!(node.name in env)) throw new Error(`unbound variable: ${node.name}`);
      const value = env[node.name];
      trace.push({ kind: 'var', id: node.id, name: node.name, value });
      return value;
    }
    case 'bin': {
      const l = run(node.left, env, trace);   // 左先求值
      const r = run(node.right, env, trace);
      const res = computeBin(node.op, l, r);
      if (!res.ok) {
        const err: DivByZero = { kind: 'DivByZero', id: node.id };
        throw err;                            // 首个错误即终止求值
      }
      return res.value;
    }
    case 'logic': {
      const l = run(node.left, env, trace);
      if (node.op === '&&') return truthy(l) ? run(node.right, env, trace) : l;
      return truthy(l) ? l : run(node.right, env, trace);
    }
    case 'cond': {
      const c = run(node.cond, env, trace);
      return truthy(c) ? run(node.then, env, trace) : run(node.els, env, trace);
    }
    case 'error': {
      // 折叠期发现的常量除零：在原本该执行到它的位置抛错，ID 不变。
      const err: DivByZero = { kind: 'DivByZero', id: node.id };
      throw err;
    }
  }
}
