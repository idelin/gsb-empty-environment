/**
 * 求值器：严格从左到右求值，&&/||/?: 短路。
 * 可观察行为被记录进 trace：
 *   - 每次变量读取产生一条 read 事件（携带变量的稳定 id 与名字）；
 *   - 求值结果 outcome：要么是一个值，要么是首个错误（携带出错操作的稳定 id）。
 * 纯常量运算不产生任何可观察事件，因此可以安全折叠。
 */

import { EvalError, Expr, Value } from './ast';

export type Env = Readonly<Record<string, Value>>;

export interface ReadEvent {
  type: 'read';
  id: number;
  name: string;
}

export type Trace = ReadEvent[];

export type Outcome =
  | { ok: true; value: Value }
  | { ok: false; error: EvalError };

export interface RunResult {
  outcome: Outcome;
  trace: Trace;
}

function err(kind: EvalError['kind'], opId: number, message: string): Outcome {
  return { ok: false, error: { kind, opId, message } };
}

export function evalExpr(e: Expr, env: Env, trace: Trace): Outcome {
  switch (e.kind) {
    case 'const':
      return { ok: true, value: e.value };

    case 'var': {
      trace.push({ type: 'read', id: e.id, name: e.name });
      if (!(e.name in env)) {
        return err('UnboundVar', e.id, `unbound variable '${e.name}'`);
      }
      return { ok: true, value: env[e.name] as Value };
    }

    case 'bin': {
      const l = evalExpr(e.left, env, trace);
      if (!l.ok) return l;
      const r = evalExpr(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof l.value !== 'number' || typeof r.value !== 'number') {
        return err('TypeError', e.id, `operator '${e.op}' expects numbers`);
      }
      if (e.op === '/' && r.value === 0) {
        return err('DivByZero', e.id, 'division by zero');
      }
      switch (e.op) {
        case '+': return { ok: true, value: l.value + r.value };
        case '-': return { ok: true, value: l.value - r.value };
        case '*': return { ok: true, value: l.value * r.value };
        case '/': return { ok: true, value: l.value / r.value };
      }
      break;
    }

    case 'and': {
      const l = evalExpr(e.left, env, trace);
      if (!l.ok) return l;
      if (typeof l.value !== 'boolean') {
        return err('TypeError', e.id, `'&&' expects boolean operands`);
      }
      if (l.value === false) return { ok: true, value: false };
      const r = evalExpr(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof r.value !== 'boolean') {
        return err('TypeError', e.id, `'&&' expects boolean operands`);
      }
      return { ok: true, value: r.value };
    }

    case 'or': {
      const l = evalExpr(e.left, env, trace);
      if (!l.ok) return l;
      if (typeof l.value !== 'boolean') {
        return err('TypeError', e.id, `'||' expects boolean operands`);
      }
      if (l.value === true) return { ok: true, value: true };
      const r = evalExpr(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof r.value !== 'boolean') {
        return err('TypeError', e.id, `'||' expects boolean operands`);
      }
      return { ok: true, value: r.value };
    }

    case 'cond': {
      const c = evalExpr(e.cond, env, trace);
      if (!c.ok) return c;
      if (typeof c.value !== 'boolean') {
        return err('TypeError', e.id, `condition expects boolean`);
      }
      return evalExpr(c.value ? e.then : e.else, env, trace);
    }
  }
  throw new Error('unreachable');
}

export function runExpr(e: Expr, env: Env): RunResult {
  const trace: Trace = [];
  const outcome = evalExpr(e, env, trace);
  return { outcome, trace };
}
