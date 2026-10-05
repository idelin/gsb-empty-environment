/**
 * 穷举差分测试：
 * 对大量表达式 × 全部小环境，分别运行原始求值器与"折叠后求值器"，
 * 比较三者：结果值 / 首个错误（含出错操作的稳定 id）/ 变量读取序列。
 *
 * 表达式集合 = 全部深度 ≤1 的表达式（穷举）
 *            + 针对性用例（零除、重复子表达式、已知条件但分支含错误等）
 *            + 固定种子的随机深层表达式。
 */

import { Builder, Expr, showExpr, Value } from './ast';
import { Env, runExpr, RunResult } from './eval';
import { exprSize, fold, optSize, runOpt, showOpt } from './fold';

const LEAF_VALUES: Value[] = [0, 1, 2, true, false];
const VAR_NAMES = ['x', 'y'];

function leaves(b: Builder): Expr[] {
  return [
    ...LEAF_VALUES.map((v) => (typeof v === 'number' ? b.num(v) : b.bool(v))),
    ...VAR_NAMES.map((n) => b.variable(n)),
  ];
}

const BIN_OPS = ['+', '-', '*', '/'] as const;

/** 深度 ≤1 的全部表达式 */
function allDepth1(): Expr[] {
  const out: Expr[] = [];
  const b1 = new Builder();
  const ls = leaves(b1);
  out.push(...ls);
  // 每个表达式需要独立的 id，因此逐棵重建
  const rebuild = (f: (b: Builder) => Expr) => {
    const b = new Builder();
    out.push(f(b));
  };
  for (let i = 0; i < ls.length; i++) {
    for (let j = 0; j < ls.length; j++) {
      for (const op of BIN_OPS) rebuild((b) => b.bin(op, leafAt(b, i), leafAt(b, j)));
      rebuild((b) => b.and(leafAt(b, i), leafAt(b, j)));
      rebuild((b) => b.or(leafAt(b, i), leafAt(b, j)));
    }
  }
  for (let i = 0; i < ls.length; i++) {
    for (let j = 0; j < ls.length; j++) {
      for (let k = 0; k < ls.length; k++) {
        rebuild((b) => b.cond(leafAt(b, i), leafAt(b, j), leafAt(b, k)));
      }
    }
  }
  return out;
}

function leafAt(b: Builder, i: number): Expr {
  const ls = leaves(b);
  return ls[i]!;
}

/** 针对性用例：覆盖题目要求的关键场景 */
function curated(): Expr[] {
  const out: Expr[] = [];
  const add = (f: (b: Builder) => Expr) => {
    const b = new Builder();
    out.push(f(b));
  };

  // —— 零除 ——
  add((b) => b.bin('/', b.num(1), b.num(0)));                       // 1/0 直接零除
  add((b) => b.bin('/', b.num(0), b.num(0)));                       // 0/0
  add((b) => b.bin('/', b.variable('x'), b.num(0)));                // x/0：读取 x 后才报错
  add((b) => b.bin('/', b.num(1), b.bin('-', b.num(2), b.num(2)))); // 1/(2-2)：折叠后才发现零除
  add((b) => b.bin('/', b.num(1), b.bin('-', b.variable('x'), b.variable('x')))); // 1/(x-x)：取决于环境
  add((b) => b.bin('/', b.bin('+', b.num(1), b.num(1)), b.bin('-', b.num(2), b.num(2)))); // (1+1)/(2-2)

  // —— 短路：未执行分支不能提前报错 ——
  add((b) => b.and(b.bool(false), b.bin('/', b.num(1), b.num(0)))); // false && (1/0) → false
  add((b) => b.and(b.bool(true), b.bin('/', b.num(1), b.num(0))));  // true && (1/0) → 错误
  add((b) => b.or(b.bool(true), b.bin('/', b.num(1), b.num(0))));   // true || (1/0) → true
  add((b) => b.or(b.bool(false), b.bin('/', b.num(1), b.num(0))));  // false || (1/0) → 错误
  add((b) => b.and(b.bin('/', b.num(1), b.num(0)), b.bool(false))); // (1/0) && false → 左侧先错
  add((b) => b.and(b.bool(false), b.variable('x')));                // false && x → 不读 x
  add((b) => b.and(b.bool(true), b.variable('x')));                 // true && x → 读 x
  add((b) => b.or(b.bool(true), b.variable('x')));                  // true || x → 不读 x
  add((b) => b.and(b.variable('x'), b.bin('/', b.num(1), b.num(0)))); // x && (1/0)：取决于 x
  add((b) => b.or(b.variable('x'), b.bin('/', b.num(1), b.num(0))));  // x || (1/0)

  // —— 条件已知但分支含错误 ——
  add((b) => b.cond(b.bool(true), b.num(1), b.bin('/', b.num(1), b.num(0))));   // true ? 1 : (1/0)
  add((b) => b.cond(b.bool(false), b.bin('/', b.num(1), b.num(0)), b.num(2)));  // false ? (1/0) : 2
  add((b) => b.cond(b.bool(true), b.bin('/', b.num(1), b.num(0)), b.num(3)));   // true ? (1/0) : 3 → 错误
  add((b) => b.cond(b.variable('x'), b.bin('/', b.num(1), b.num(0)), b.num(0))); // x ? (1/0) : 0
  add((b) => b.cond(b.and(b.bool(true), b.bool(false)), b.bin('/', b.num(1), b.num(0)), b.num(9))); // (true&&false) ? (1/0) : 9

  // —— 重复子表达式 ——
  add((b) => { // (x+1)*(x+1)：两次读取都必须保留
    const sub = () => b.bin('+', b.variable('x'), b.num(1));
    return b.bin('*', sub(), sub());
  });
  add((b) => { // (0/0)+(0/0)：结构完全相同的两棵子树，首个错误必须来自左边那棵的 id
    const sub = () => b.bin('/', b.num(0), b.num(0));
    return b.bin('+', sub(), sub());
  });
  add((b) => { // (1/0)&&(1/0)：错误 id 必须是左侧的
    const sub = () => b.bin('/', b.num(1), b.num(0));
    return b.and(sub(), sub());
  });
  add((b) => { // ((1+2)*(3+4)) + ((1+2)*(3+4))：整棵常量重复子表达式
    const sub = () => b.bin('*', b.bin('+', b.num(1), b.num(2)), b.bin('+', b.num(3), b.num(4)));
    return b.bin('+', sub(), sub());
  });

  // —— 类型错误与求值顺序 ——
  add((b) => b.and(b.num(1), b.bin('/', b.num(1), b.num(0))));  // 1 && (1/0)：类型错误先于零除
  add((b) => b.bin('+', b.bool(true), b.bin('/', b.num(1), b.num(0)))); // true + (1/0)：类型错误
  add((b) => b.bin('+', b.bin('/', b.num(1), b.num(0)), b.bool(true))); // (1/0) + true：零除在前
  add((b) => b.cond(b.num(0), b.num(1), b.num(2)));             // 0 ? 1 : 2：条件类型错误

  // —— 嵌套组合 ——
  add((b) => // ((x && true) || (false && (1/0))) ? (10/(2-2)) : (x*2)
    b.cond(
      b.or(b.and(b.variable('x'), b.bool(true)), b.and(b.bool(false), b.bin('/', b.num(1), b.num(0)))),
      b.bin('/', b.num(10), b.bin('-', b.num(2), b.num(2))),
      b.bin('*', b.variable('x'), b.num(2)),
    ));
  return out;
}

/** 固定种子随机表达式（深度 ≤4），保证可复现 */
function randomExprs(count: number, seed: number): Expr[] {
  let s = seed >>> 0;
  const rand = () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
  const pick = <T>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)]!;
  const out: Expr[] = [];
  for (let n = 0; n < count; n++) {
    const b = new Builder();
    const gen = (depth: number): Expr => {
      if (depth === 0 || rand() < 0.3) {
        return pick([0, 1, 2, 3, true, false].map((v) => () =>
          typeof v === 'number' ? b.num(v) : b.bool(v),
        ).concat([() => b.variable('x'), () => b.variable('y')]))();
      }
      const kind = pick(['bin', 'and', 'or', 'cond'] as const);
      if (kind === 'cond') return b.cond(gen(depth - 1), gen(depth - 1), gen(depth - 1));
      if (kind === 'bin') return b.bin(pick(BIN_OPS), gen(depth - 1), gen(depth - 1));
      if (kind === 'and') return b.and(gen(depth - 1), gen(depth - 1));
      return b.or(gen(depth - 1), gen(depth - 1));
    };
    out.push(gen(4));
  }
  return out;
}

function environments(): Env[] {
  const envs: Env[] = [];
  const domain: Value[] = [0, 1, 2, true, false];
  for (const x of domain) {
    for (const y of domain) {
      envs.push({ x, y });
    }
  }
  envs.push({});       // 未绑定变量
  envs.push({ x: 1 }); // 部分绑定
  return envs;
}

function fmtResult(r: RunResult): string {
  const trace = r.trace.map((t) => `read(${t.name}#${t.id})`).join(' ');
  const outcome = r.outcome.ok
    ? `value=${JSON.stringify(r.outcome.value)}`
    : `error=${r.outcome.error.kind}@#${r.outcome.error.opId}`;
  return `${outcome} | trace=[${trace}]`;
}

function sameResult(a: RunResult, b: RunResult): boolean {
  if (a.outcome.ok !== b.outcome.ok) return false;
  if (a.outcome.ok && b.outcome.ok && a.outcome.value !== b.outcome.value) return false;
  if (!a.outcome.ok && !b.outcome.ok) {
    if (a.outcome.error.kind !== b.outcome.error.kind) return false;
    if (a.outcome.error.opId !== b.outcome.error.opId) return false;
  }
  if (a.trace.length !== b.trace.length) return false;
  for (let i = 0; i < a.trace.length; i++) {
    const ta = a.trace[i]!;
    const tb = b.trace[i]!;
    if (ta.id !== tb.id || ta.name !== tb.name) return false;
  }
  return true;
}

function main(): void {
  const exprs = [...allDepth1(), ...curated(), ...randomExprs(5000, 20261005)];
  const envs = environments();

  let checks = 0;
  let failures = 0;
  let foldedSmaller = 0;
  let foldedToLeaf = 0;

  for (const e of exprs) {
    const opt = fold(e);
    if (optSize(opt) < exprSize(e)) foldedSmaller++;
    if (opt.kind === 'const' || opt.kind === 'error') foldedToLeaf++;
    for (const env of envs) {
      const before = runExpr(e, env);
      const after = runOpt(opt, env);
      checks++;
      if (!sameResult(before, after)) {
        failures++;
        if (failures <= 20) {
          console.error('MISMATCH');
          console.error(`  expr: ${showExpr(e)}`);
          console.error(`  opt : ${showOpt(opt)}`);
          console.error(`  env : ${JSON.stringify(env)}`);
          console.error(`  before: ${fmtResult(before)}`);
          console.error(`  after : ${fmtResult(after)}`);
        }
      }
    }
  }

  console.log(`expressions: ${exprs.length} (depth<=1 exhaustive + curated + random)`);
  console.log(`environments: ${envs.length}`);
  console.log(`total checks: ${checks}`);
  console.log(`folded smaller: ${foldedSmaller}, folded to single leaf: ${foldedToLeaf}`);
  if (failures > 0) {
    console.error(`FAILURES: ${failures}`);
    process.exit(1);
  }
  console.log('ALL OK: 值 / 首个错误(含稳定id) / 变量读取序列 在优化前后完全一致');
}

main();
