/**
 * 常量折叠优化器。
 *
 * 安全性论证（为什么折叠不改变可观察行为）：
 *  - 唯一被折叠掉的是"纯常量计算"：它既不读变量，也不会在运行时才出错
 *    （所有可能的错误——零除、类型错误——都在折叠期算好，变成 error 常量节点，
 *     只有真正被执行到时才抛出，且携带原操作的稳定 id）。
 *  - 短路规则只在"被跳过的分支永远不会被执行"时触发（如 false && _、true || _、
 *    已知条件的 ?:），因此未执行分支里的读取/错误不会被提前。
 *  - 不做任何代数恒等变换（如 x+0 → x）：常量之外的节点一律保留，
 *    变量读取与求值顺序完全不变。
 *
 * 输出 OptExpr：保留的节点沿用原稳定 id；折叠产生的新叶子（const/error）
 * 通过 src 字段记录它由哪些来源节点折叠而来，实现优化树与来源的对应。
 */

import { BinOp, EvalError, Expr, Value } from './ast';
import { Env, Outcome, RunResult, Trace } from './eval';

export type OptExpr =
  | { kind: 'const'; value: Value; src: number[] }
  | { kind: 'error'; error: EvalError; src: number[] }
  | { kind: 'var'; id: number; name: string; src: number[] }
  | { kind: 'bin'; id: number; op: BinOp; left: OptExpr; right: OptExpr; src: number[] }
  | { kind: 'and'; id: number; left: OptExpr; right: OptExpr; src: number[] }
  | { kind: 'or'; id: number; left: OptExpr; right: OptExpr; src: number[] }
  | { kind: 'cond'; id: number; cond: OptExpr; then: OptExpr; else: OptExpr; src: number[] };

/** 用 extra 中的来源 id 扩充节点（返回浅拷贝，不改动原节点） */
function absorb(node: OptExpr, extra: number[]): OptExpr {
  return { ...node, src: [...node.src, ...extra] };
}

function constNode(value: Value, src: number[]): OptExpr {
  return { kind: 'const', value, src };
}

function errorNode(kind: EvalError['kind'], opId: number, message: string, src: number[]): OptExpr {
  return { kind: 'error', error: { kind, opId, message }, src };
}

export function fold(e: Expr): OptExpr {
  switch (e.kind) {
    case 'const':
      return constNode(e.value, [e.id]);

    case 'var':
      return { kind: 'var', id: e.id, name: e.name, src: [e.id] };

    case 'bin': {
      const l = fold(e.left);
      const r = fold(e.right);
      // 左操作数先求值：它若已是常量错误，整个表达式就是该错误
      if (l.kind === 'error') return absorb(l, [e.id]);
      if (l.kind === 'const') {
        // 左操作数是纯常量（无可观察行为），右侧错误即整个表达式的行为
        if (r.kind === 'error') return absorb(r, [e.id]);
        if (r.kind === 'const') {
          // 两个都是常量：折叠期完成计算，错误变成 error 常量节点
          if (typeof l.value !== 'number' || typeof r.value !== 'number') {
            return errorNode('TypeError', e.id, `operator '${e.op}' expects numbers`,
              [...l.src, ...r.src, e.id]);
          }
          if (e.op === '/' && r.value === 0) {
            return errorNode('DivByZero', e.id, 'division by zero',
              [...l.src, ...r.src, e.id]);
          }
          const v =
            e.op === '+' ? l.value + r.value :
            e.op === '-' ? l.value - r.value :
            e.op === '*' ? l.value * r.value :
                           l.value / r.value;
          return constNode(v, [...l.src, ...r.src, e.id]);
        }
      }
      return { kind: 'bin', id: e.id, op: e.op, left: l, right: r, src: [e.id] };
    }

    case 'and': {
      const l = fold(e.left);
      if (l.kind === 'error') return absorb(l, [e.id]);
      const r = fold(e.right);
      if (l.kind === 'const') {
        if (typeof l.value !== 'boolean') {
          // 左操作数类型错误在右操作数求值之前发生
          return errorNode('TypeError', e.id, `'&&' expects boolean operands`,
            [...l.src, e.id]);
        }
        if (l.value === false) {
          // 短路：右操作数永远不会被执行，整体恒为 false
          return constNode(false, [...l.src, e.id]);
        }
        // true && r：仅当 r 的行为已完全确定时才能消去 && 节点
        // （&& 还会对右操作数做布尔类型检查，如 true && 0 是 TypeError）
        if (r.kind === 'error') return absorb(r, [...l.src, e.id]);
        if (r.kind === 'const') {
          if (typeof r.value !== 'boolean') {
            return errorNode('TypeError', e.id, `'&&' expects boolean operands`,
              [...l.src, ...r.src, e.id]);
          }
          return constNode(r.value, [...l.src, ...r.src, e.id]);
        }
      }
      return { kind: 'and', id: e.id, left: l, right: r, src: [e.id] };
    }

    case 'or': {
      const l = fold(e.left);
      if (l.kind === 'error') return absorb(l, [e.id]);
      const r = fold(e.right);
      if (l.kind === 'const') {
        if (typeof l.value !== 'boolean') {
          return errorNode('TypeError', e.id, `'||' expects boolean operands`,
            [...l.src, e.id]);
        }
        if (l.value === true) {
          return constNode(true, [...l.src, e.id]);
        }
        // false || r：与 && 同理，仅当 r 行为确定时才能消去 || 节点
        if (r.kind === 'error') return absorb(r, [...l.src, e.id]);
        if (r.kind === 'const') {
          if (typeof r.value !== 'boolean') {
            return errorNode('TypeError', e.id, `'||' expects boolean operands`,
              [...l.src, ...r.src, e.id]);
          }
          return constNode(r.value, [...l.src, ...r.src, e.id]);
        }
      }
      return { kind: 'or', id: e.id, left: l, right: r, src: [e.id] };
    }

    case 'cond': {
      const c = fold(e.cond);
      if (c.kind === 'error') return absorb(c, [e.id]);
      if (c.kind === 'const') {
        if (typeof c.value !== 'boolean') {
          return errorNode('TypeError', e.id, `condition expects boolean`,
            [...c.src, e.id]);
        }
        // 条件已知：未选中的分支永远不会被执行，直接丢弃
        const branch = fold(c.value ? e.then : e.else);
        return absorb(branch, [...c.src, e.id]);
      }
      const t = fold(e.then);
      const f = fold(e.else);
      return { kind: 'cond', id: e.id, cond: c, then: t, else: f, src: [e.id] };
    }
  }
}

/** 优化树的求值器：语义与 evalExpr 完全一致，只是常量错误在"被执行到时"才抛出 */
export function evalOpt(e: OptExpr, env: Env, trace: Trace): Outcome {
  switch (e.kind) {
    case 'const':
      return { ok: true, value: e.value };

    case 'error':
      return { ok: false, error: e.error };

    case 'var': {
      trace.push({ type: 'read', id: e.id, name: e.name });
      if (!(e.name in env)) {
        return { ok: false, error: { kind: 'UnboundVar', opId: e.id, message: `unbound variable '${e.name}'` } };
      }
      return { ok: true, value: env[e.name] as Value };
    }

    case 'bin': {
      const l = evalOpt(e.left, env, trace);
      if (!l.ok) return l;
      const r = evalOpt(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof l.value !== 'number' || typeof r.value !== 'number') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `operator '${e.op}' expects numbers` } };
      }
      if (e.op === '/' && r.value === 0) {
        return { ok: false, error: { kind: 'DivByZero', opId: e.id, message: 'division by zero' } };
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
      const l = evalOpt(e.left, env, trace);
      if (!l.ok) return l;
      if (typeof l.value !== 'boolean') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `'&&' expects boolean operands` } };
      }
      if (l.value === false) return { ok: true, value: false };
      const r = evalOpt(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof r.value !== 'boolean') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `'&&' expects boolean operands` } };
      }
      return { ok: true, value: r.value };
    }

    case 'or': {
      const l = evalOpt(e.left, env, trace);
      if (!l.ok) return l;
      if (typeof l.value !== 'boolean') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `'||' expects boolean operands` } };
      }
      if (l.value === true) return { ok: true, value: true };
      const r = evalOpt(e.right, env, trace);
      if (!r.ok) return r;
      if (typeof r.value !== 'boolean') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `'||' expects boolean operands` } };
      }
      return { ok: true, value: r.value };
    }

    case 'cond': {
      const c = evalOpt(e.cond, env, trace);
      if (!c.ok) return c;
      if (typeof c.value !== 'boolean') {
        return { ok: false, error: { kind: 'TypeError', opId: e.id, message: `condition expects boolean` } };
      }
      return evalOpt(c.value ? e.then : e.else, env, trace);
    }
  }
  throw new Error('unreachable');
}

export function runOpt(e: OptExpr, env: Env): RunResult {
  const trace: Trace = [];
  const outcome = evalOpt(e, env, trace);
  return { outcome, trace };
}

export function showOpt(e: OptExpr): string {
  switch (e.kind) {
    case 'const':
      return `${JSON.stringify(e.value)}`;
    case 'error':
      return `<error ${e.error.kind}@#${e.error.opId}>`;
    case 'var':
      return `${e.name}#${e.id}`;
    case 'bin':
      return `(${showOpt(e.left)} ${e.op}#${e.id} ${showOpt(e.right)})`;
    case 'and':
      return `(${showOpt(e.left)} &&#${e.id} ${showOpt(e.right)})`;
    case 'or':
      return `(${showOpt(e.left)} ||#${e.id} ${showOpt(e.right)})`;
    case 'cond':
      return `(${showOpt(e.cond)} ?#${e.id} ${showOpt(e.then)} : ${showOpt(e.else)})`;
  }
}

/** 统计优化树节点数（用于展示折叠效果） */
export function optSize(e: OptExpr): number {
  switch (e.kind) {
    case 'const':
    case 'error':
    case 'var':
      return 1;
    case 'bin':
    case 'and':
    case 'or':
      return 1 + optSize(e.left) + optSize(e.right);
    case 'cond':
      return 1 + optSize(e.cond) + optSize(e.then) + optSize(e.else);
  }
}

export function exprSize(e: Expr): number {
  switch (e.kind) {
    case 'const':
    case 'var':
      return 1;
    case 'bin':
    case 'and':
    case 'or':
      return 1 + exprSize(e.left) + exprSize(e.right);
    case 'cond':
      return 1 + exprSize(e.cond) + exprSize(e.then) + exprSize(e.else);
  }
}
