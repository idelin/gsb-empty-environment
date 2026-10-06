// 解释器：溢出与除零是可见错误（IRError），并记录执行轨迹。
// 轨迹 = 依次进入的语句 id 序列；出错时末尾追加 { type: 'error', id, kind }。

import { Expr, Stmt, Program, BinOp, isI32 } from './ir';

export type ErrorKind = 'overflow' | 'divzero' | 'undef';

export class IRError extends Error {
  constructor(public kind: ErrorKind, public at: number) {
    super(`${kind} at node #${at}`);
    this.name = 'IRError';
  }
}

export type TraceEntry =
  | { type: 'stmt'; id: number }
  | { type: 'error'; id: number; kind: ErrorKind };

export type Outcome =
  | { type: 'ok'; value: number; trace: TraceEntry[] }
  | { type: 'err'; kind: ErrorKind; at: number; trace: TraceEntry[] };

function applyBin(op: BinOp, l: number, r: number, at: number): number {
  switch (op) {
    case 'add':
    case 'sub':
    case 'mul': {
      const v = op === 'add' ? l + r : op === 'sub' ? l - r : l * r;
      if (!isI32(v)) throw new IRError('overflow', at);
      return v;
    }
    case 'div': {
      if (r === 0) throw new IRError('divzero', at);
      const v = Math.trunc(l / r); // 向零取整
      if (!isI32(v)) throw new IRError('overflow', at); // I32_MIN / -1
      return v;
    }
    case 'mod': {
      if (r === 0) throw new IRError('divzero', at);
      return l % r; // I32_MIN % -1 === 0，不溢出
    }
    case 'eq': return l === r ? 1 : 0;
    case 'ne': return l !== r ? 1 : 0;
    case 'lt': return l < r ? 1 : 0;
    case 'le': return l <= r ? 1 : 0;
    case 'gt': return l > r ? 1 : 0;
    case 'ge': return l >= r ? 1 : 0;
  }
}

function evalExpr(e: Expr, env: Map<string, number>): number {
  switch (e.kind) {
    case 'const':
      return e.value;
    case 'var': {
      const v = env.get(e.name);
      if (v === undefined) throw new IRError('undef', e.id);
      return v;
    }
    case 'neg': {
      const v = -evalExpr(e.e, env);
      if (!isI32(v)) throw new IRError('overflow', e.id); // -I32_MIN
      return v;
    }
    case 'bin': {
      const l = evalExpr(e.l, env);
      const r = evalExpr(e.r, env);
      return applyBin(e.op, l, r, e.id);
    }
  }
}

// 返回 return 语句产生的值；undefined 表示本块未返回（继续fall through）
function execBlock(
  body: Stmt[],
  env: Map<string, number>,
  trace: TraceEntry[],
): number | undefined {
  for (const s of body) {
    trace.push({ type: 'stmt', id: s.id });
    switch (s.kind) {
      case 'assign':
        env.set(s.name, evalExpr(s.e, env));
        break;
      case 'if': {
        const c = evalExpr(s.cond, env);
        const r = execBlock(c !== 0 ? s.then : s.els, env, trace);
        if (r !== undefined) return r;
        break;
      }
      case 'return':
        return evalExpr(s.e, env);
    }
  }
  return undefined;
}

export function run(prog: Program, inputs: Record<string, number>): Outcome {
  const env = new Map<string, number>();
  for (const name of prog.inputs) {
    const v = inputs[name];
    if (v === undefined || !isI32(v)) throw new Error(`bad input for ${name}`);
    env.set(name, v);
  }
  const trace: TraceEntry[] = [];
  try {
    const ret = execBlock(prog.body, env, trace);
    if (ret === undefined) throw new Error('program fell through without return');
    return { type: 'ok', value: ret, trace };
  } catch (err) {
    if (err instanceof IRError) {
      trace.push({ type: 'error', id: err.at, kind: err.kind });
      return { type: 'err', kind: err.kind, at: err.at, trace };
    }
    throw err;
  }
}
