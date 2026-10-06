// 优化器：常量传播 + 常量折叠 + 不可达分支/死代码删除。
//
// 正确性约束（溢出与除零是可见错误）：
//  1. 只折叠“全部为常量且求值不会出错”的表达式；会出错的常量表达式
//     （如 1/0、I32_MIN/-1、-I32_MIN）保留原节点，让错误在运行时同一 id 处发生。
//  2. 绝不做会丢弃非恒定子表达式的化简：x*0、0*x、0/e、e-e 等一律不折叠，
//     因为被丢弃的子表达式可能抛出溢出/除零。只允许恒等元化简（e+0、e-0、
//     e*1、e/1），它们保留对 e 的恰好一次求值。
//  3. 只删除“确定不可达”的分支：条件被常量传播证明为常量时，删除另一分支；
//     语句在无条件 return 之后同理。保留 if 节点本身（死分支置空），
//     使执行轨迹中的语句 id 与原程序一致。

import { Expr, Stmt, Program, BinOp, isI32 } from './ir';

const UNKNOWN = Symbol('unknown');
type MaybeConst = number | typeof UNKNOWN;
type ConstEnv = Map<string, MaybeConst>;

function isConst(e: Expr, v: number): boolean {
  return e.kind === 'const' && e.value === v;
}

// 常量求值；会出错（溢出/除零）时返回 null，表示“保留原节点，运行时再报错”
function tryConstBin(op: BinOp, l: number, r: number): number | null {
  switch (op) {
    case 'add': { const v = l + r; return isI32(v) ? v : null; }
    case 'sub': { const v = l - r; return isI32(v) ? v : null; }
    case 'mul': { const v = l * r; return isI32(v) ? v : null; }
    case 'div': {
      if (r === 0) return null;
      const v = Math.trunc(l / r);
      return isI32(v) ? v : null;
    }
    case 'mod': {
      if (r === 0) return null;
      return l % r;
    }
    case 'eq': return l === r ? 1 : 0;
    case 'ne': return l !== r ? 1 : 0;
    case 'lt': return l < r ? 1 : 0;
    case 'le': return l <= r ? 1 : 0;
    case 'gt': return l > r ? 1 : 0;
    case 'ge': return l >= r ? 1 : 0;
  }
}

function optExpr(e: Expr, env: ConstEnv): Expr {
  switch (e.kind) {
    case 'const':
      return e;
    case 'var': {
      const v = env.get(e.name);
      if (v !== undefined && v !== UNKNOWN) {
        return { kind: 'const', id: e.id, value: v };
      }
      return e;
    }
    case 'neg': {
      const inner = optExpr(e.e, env);
      if (inner.kind === 'const') {
        const v = -inner.value;
        if (isI32(v)) return { kind: 'const', id: e.id, value: v };
        return { kind: 'neg', id: e.id, e: inner }; // -I32_MIN：保留，运行时溢出
      }
      return { kind: 'neg', id: e.id, e: inner };
    }
    case 'bin': {
      const l = optExpr(e.l, env);
      const r = optExpr(e.r, env);
      if (l.kind === 'const' && r.kind === 'const') {
        const v = tryConstBin(e.op, l.value, r.value);
        if (v !== null) return { kind: 'const', id: e.id, value: v };
        return { kind: 'bin', id: e.id, op: e.op, l, r }; // 运行时报错，保留节点
      }
      // 恒等元化简：保留对非恒定子表达式的恰好一次求值，安全。
      if (e.op === 'add' && isConst(r, 0)) return l;
      if (e.op === 'add' && isConst(l, 0)) return r;
      if (e.op === 'sub' && isConst(r, 0)) return l;
      if (e.op === 'mul' && isConst(r, 1)) return l;
      if (e.op === 'mul' && isConst(l, 1)) return r;
      if (e.op === 'div' && isConst(r, 1)) return l;
      // 明确不做：e*0、0*e、0/e、e/e、e-e 等会丢弃求值的化简。
      return { kind: 'bin', id: e.id, op: e.op, l, r };
    }
  }
}

interface StmtResult {
  stmts: Stmt[];
  fallsThrough: boolean; // 执行完这些语句后是否可能继续往下走
  env: ConstEnv;         // fallsThrough 时的出口常量环境
}

function mergeEnvs(envs: ConstEnv[]): ConstEnv {
  const out: ConstEnv = new Map();
  if (envs.length === 0) return out;
  const keys = new Set<string>();
  for (const e of envs) for (const k of e.keys()) keys.add(k);
  for (const k of keys) {
    let merged: MaybeConst = UNKNOWN;
    let first = true;
    for (const e of envs) {
      const raw = e.get(k);
      const v: MaybeConst = raw === undefined ? UNKNOWN : raw;
      if (first) { merged = v; first = false; }
      else if (merged !== v) { merged = UNKNOWN; break; }
    }
    out.set(k, merged);
  }
  return out;
}

function optStmt(s: Stmt, env: ConstEnv): StmtResult {
  switch (s.kind) {
    case 'assign': {
      const e = optExpr(s.e, env);
      const next: ConstEnv = new Map(env);
      next.set(s.name, e.kind === 'const' ? e.value : UNKNOWN);
      // 赋值语句保留（即使右值是常量），保证轨迹一致。
      return { stmts: [{ kind: 'assign', id: s.id, name: s.name, e }], fallsThrough: true, env: next };
    }
    case 'return':
      return { stmts: [{ kind: 'return', id: s.id, e: optExpr(s.e, env) }], fallsThrough: false, env };
    case 'if': {
      const cond = optExpr(s.cond, env);
      if (cond.kind === 'const') {
        // 条件被证明为常量：另一分支确定不可达，删除。
        const taken = cond.value !== 0 ? s.then : s.els;
        const r = optBlock(taken, new Map(env));
        const kept: Stmt = cond.value !== 0
          ? { kind: 'if', id: s.id, cond, then: r.stmts, els: [] }
          : { kind: 'if', id: s.id, cond, then: [], els: r.stmts };
        return { stmts: [kept], fallsThrough: r.fallsThrough, env: r.env };
      }
      const thenR = optBlock(s.then, new Map(env));
      const elsR = optBlock(s.els, new Map(env));
      const kept: Stmt = { kind: 'if', id: s.id, cond, then: thenR.stmts, els: elsR.stmts };
      const fallsThrough = thenR.fallsThrough || elsR.fallsThrough;
      const liveEnvs: ConstEnv[] = [];
      if (thenR.fallsThrough) liveEnvs.push(thenR.env);
      if (elsR.fallsThrough) liveEnvs.push(elsR.env);
      return { stmts: [kept], fallsThrough, env: mergeEnvs(liveEnvs) };
    }
  }
}

function optBlock(body: Stmt[], env: ConstEnv): StmtResult {
  const out: Stmt[] = [];
  let cur = env;
  for (const s of body) {
    const r = optStmt(s, cur);
    out.push(...r.stmts);
    cur = r.env;
    if (!r.fallsThrough) {
      // 之后的语句确定不可达，删除。
      return { stmts: out, fallsThrough: false, env: cur };
    }
  }
  return { stmts: out, fallsThrough: true, env: cur };
}

export function optimize(prog: Program): Program {
  const env: ConstEnv = new Map();
  for (const name of prog.inputs) env.set(name, UNKNOWN);
  const r = optBlock(prog.body, env);
  return { body: r.stmts, inputs: prog.inputs.slice() };
}

// 统计节点数（用于报告优化效果）
export function countNodes(prog: Program): number {
  let n = 0;
  const walkE = (e: Expr): void => {
    n++;
    if (e.kind === 'neg') walkE(e.e);
    else if (e.kind === 'bin') { walkE(e.l); walkE(e.r); }
  };
  const walkS = (ss: Stmt[]): void => {
    for (const s of ss) {
      n++;
      if (s.kind === 'assign' || s.kind === 'return') walkE(s.e);
      else { walkE(s.cond); walkS(s.then); walkS(s.els); }
    }
  };
  walkS(prog.body);
  return n;
}
