// 测试：单元测试 + 已知答案程序 + 随机程序模糊对照（符号执行 vs 全输入穷举）。

import {
  add,
  assertThat,
  assign,
  bin,
  iff,
  inp,
  land,
  lnot,
  lor,
  mul,
  neg,
  num,
  program,
  resetIds,
  sub,
  v,
  whileDo,
  wrap8,
} from "../src/ast.ts";
import type { BinOp, Expr, Program, Stmt } from "../src/ast.ts";
import { crossCheck } from "../src/check.ts";
import { demoProgram, safeProgram, unreachableProgram } from "../src/examples.ts";
import { findMin } from "../src/solve.ts";
import { sCmp, sIn, sc } from "../src/sym.ts";

let passed = 0;
let failed = 0;
function check(name: string, cond: boolean): void {
  if (cond) {
    passed++;
    console.log(`ok   - ${name}`);
  } else {
    failed++;
    console.log(`FAIL - ${name}`);
  }
}

// ---------- 单元测试 ----------

check("wrap8(127) = 127", wrap8(127) === 127);
check("wrap8(128) = -128", wrap8(128) === -128);
check("wrap8(-129) = 127", wrap8(-129) === 127);
check("wrap8(255) = -1", wrap8(255) === -1);
check("wrap8(256) = 0", wrap8(256) === 0);

{
  const min = findMin(sCmp("eq", sIn("x"), sc(5)), ["x"]);
  check("findMin(x == 5) = 5", min !== null && min["x"] === 5);
}
{
  // x > 127 在 8 位有符号下不可满足
  const min = findMin(sCmp("gt", sIn("x"), sc(127)), ["x"]);
  check("findMin(x > 127) = null", min === null);
}

// ---------- 已知答案程序 ----------

// 合并：if (x < 0) y = 10 else y = 20; assert(y != 10)
// 失败 iff x < 0，最小输入为字节 0x80 即 x = -128。
{
  resetIds();
  const p = program(
    ["x"],
    [
      iff(bin("lt", inp("x"), num(0)), [assign("y", num(10))], [assign("y", num(20))]),
      assertThat(bin("ne", v("y"), num(10)), "y != 10"),
    ],
    4,
  );
  const r = crossCheck(p);
  check("merge: 最小输入 x = -128", r.minSym !== null && r.minSym["x"] === -128);
  check("merge: 失败输入 128 个", r.failSymCount === 128);
  check("merge: 穷举对照一致", r.ok);
}

// 循环：i = 0; s = 0; while (i < 3) { s += x; i += 1 }; assert(s != 30)
// 失败 iff 3x ≡ 30 (mod 256) iff x = 10。
{
  resetIds();
  const p = program(
    ["x"],
    [
      assign("i", num(0)),
      assign("s", num(0)),
      whileDo(bin("lt", v("i"), num(3)), [
        assign("s", add(v("s"), inp("x"))),
        assign("i", add(v("i"), num(1))),
      ]),
      assertThat(bin("ne", v("s"), num(30)), "s != 30"),
    ],
    8,
  );
  const r = crossCheck(p);
  check("loop: 最小输入 x = 10", r.minSym !== null && r.minSym["x"] === 10);
  check("loop: 失败输入 1 个", r.failSymCount === 1);
  check("loop: 穷举对照一致", r.ok);
}

// 溢出：a = x + 100; assert(a < 120)
// 失败 iff x ∈ [20, 27]（x ≥ 28 时 a 回绕为负，断言通过）。
{
  resetIds();
  const p = program(
    ["x"],
    [assign("a", add(inp("x"), num(100))), assertThat(bin("lt", v("a"), num(120)), "a < 120")],
    4,
  );
  const r = crossCheck(p);
  check("overflow: 最小输入 x = 20", r.minSym !== null && r.minSym["x"] === 20);
  check("overflow: 失败输入 8 个", r.failSymCount === 8);
  check("overflow: 检测到可能溢出", r.overflowChecks.some((o) => o.possibleSym));
  check("overflow: 穷举对照一致", r.ok);
}

// 循环越界：while (x != 0) { y = x }  —— x ≠ 0 时永不退出。
// 越界输入 255 个；唯一到达断言的是 x = 0，且断言 x != 0 失败。
{
  resetIds();
  const p = program(
    ["x"],
    [
      whileDo(bin("ne", inp("x"), num(0)), [assign("y", inp("x"))]),
      assertThat(bin("ne", inp("x"), num(0)), "x != 0"),
    ],
    4,
  );
  const r = crossCheck(p);
  check("bound: 越界输入 255 个", r.boundSymCount === 255);
  check("bound: 最小输入 x = 0", r.minSym !== null && r.minSym["x"] === 0);
  check("bound: 穷举对照一致", r.ok);
}

// ---------- 示例程序 ----------

{
  const r = crossCheck(demoProgram());
  check(
    "demo: 最小输入 x=0, y=-120",
    r.minSym !== null && r.minSym["x"] === 0 && r.minSym["y"] === -120,
  );
  check("demo: 穷举对照一致", r.ok);
}
{
  const r = crossCheck(unreachableProgram());
  const unreach = r.branchChecks.filter((b) => !b.reachableSym);
  check("unreachable: 检出 2 个不可达分支臂", unreach.length === 2);
  check("unreachable: 无失败输入", r.minSym === null);
  check("unreachable: 穷举对照一致", r.ok);
}
{
  const r = crossCheck(safeProgram());
  check("safe: 无失败输入", r.minSym === null && r.failSymCount === 0);
  check("safe: 穷举对照一致", r.ok);
}

// ---------- 随机程序模糊对照 ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

class Gen {
  rnd: () => number;
  vars: string[];
  inputs: string[];
  constructor(rnd: () => number, vars: string[], inputs: string[]) {
    this.rnd = rnd;
    this.vars = vars;
    this.inputs = inputs;
  }
  pick<T>(xs: T[]): T {
    return xs[Math.floor(this.rnd() * xs.length)];
  }
  expr(depth: number): Expr {
    const r = this.rnd();
    if (depth <= 0 || r < 0.35) {
      const kind = this.pick(["num", "var", "input"]);
      if (kind === "num") return num(Math.floor(this.rnd() * 256) - 128);
      if (kind === "var") return v(this.pick(this.vars));
      return inp(this.pick(this.inputs));
    }
    const kind = this.pick(["bin", "bin", "bin", "land", "lor", "lnot", "neg"]);
    if (kind === "bin") {
      const ops: BinOp[] = ["add", "sub", "mul", "and", "or", "xor", "eq", "ne", "lt", "le", "gt", "ge"];
      return bin(this.pick(ops), this.expr(depth - 1), this.expr(depth - 1));
    }
    if (kind === "land") return land(this.expr(depth - 1), this.expr(depth - 1));
    if (kind === "lor") return lor(this.expr(depth - 1), this.expr(depth - 1));
    if (kind === "lnot") return lnot(this.expr(depth - 1));
    return neg(this.expr(depth - 1));
  }
  stmts(depth: number, count: number): Stmt[] {
    const out: Stmt[] = [];
    for (let i = 0; i < count; i++) {
      const r = this.rnd();
      if (r < 0.45 || depth <= 0) {
        out.push(assign(this.pick(this.vars), this.expr(2)));
      } else if (r < 0.65) {
        out.push(iff(this.expr(2), this.stmts(depth - 1, 2), this.stmts(depth - 1, 2)));
      } else if (r < 0.8) {
        out.push(whileDo(this.expr(2), [assign(this.pick(this.vars), this.expr(2))]));
      } else {
        out.push(assertThat(this.expr(2), `assert#${i}`));
      }
    }
    return out;
  }
}

const FUZZ_PROGRAMS = 8;
for (let seed = 1; seed <= FUZZ_PROGRAMS; seed++) {
  resetIds();
  const gen = new Gen(mulberry32(seed * 7919), ["p", "q", "r"], ["x", "y"]);
  const p = program(["x", "y"], gen.stmts(2, 6), 4);
  const r = crossCheck(p);
  check(`fuzz#${seed}: 穷举对照一致（失败 ${r.failSymCount} 个输入）`, r.ok);
}

console.log(`\n${passed} 通过, ${failed} 失败`);
process.exit(failed === 0 ? 0 : 1);
