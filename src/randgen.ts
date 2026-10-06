// 带种子的随机程序生成器：保证变量先定义后使用，数值偏向边界值，
// 以高概率触发溢出、除零、常量分支与死代码。

import { Expr, Stmt, Program, BinOp, I32_MIN, I32_MAX } from './ir';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface Generated {
  prog: Program;
  inputs: Record<string, number>;
}

const ARITH: BinOp[] = ['add', 'sub', 'mul', 'div', 'mod'];
const CMP: BinOp[] = ['eq', 'ne', 'lt', 'le', 'gt', 'ge'];

class Gen {
  private rnd: () => number;
  private nextId = 1;
  private varCounter = 0;

  constructor(seed: number) {
    this.rnd = mulberry32(seed);
  }

  private id(): number { return this.nextId++; }

  private pick<T>(arr: T[]): T {
    return arr[Math.floor(this.rnd() * arr.length)];
  }

  private chance(p: number): boolean {
    return this.rnd() < p;
  }

  private interestingConst(): number {
    const pool = [
      0, 1, -1, 2, -2, 3,
      I32_MAX, I32_MIN, I32_MAX - 1, I32_MIN + 1,
      46340, 46341, // sqrt(I32_MAX) 附近，平方即溢出
      65536, 100000,
    ];
    if (this.chance(0.7)) return this.pick(pool);
    // 随机小整数
    return Math.floor(this.rnd() * 41) - 20;
  }

  private genExpr(depth: number, defined: string[]): Expr {
    if (depth <= 0 || this.chance(0.35)) {
      if (defined.length > 0 && this.chance(0.55)) {
        return { kind: 'var', id: this.id(), name: this.pick(defined) };
      }
      return { kind: 'const', id: this.id(), value: this.interestingConst() };
    }
    if (this.chance(0.12)) {
      return { kind: 'neg', id: this.id(), e: this.genExpr(depth - 1, defined) };
    }
    const op = this.pick(ARITH);
    return {
      kind: 'bin',
      id: this.id(),
      op,
      l: this.genExpr(depth - 1, defined),
      r: this.genExpr(depth - 1, defined),
    };
  }

  private genCond(defined: string[]): Expr {
    // 条件用比较运算；偶尔直接用算术表达式（非零即真）
    if (this.chance(0.8)) {
      return {
        kind: 'bin',
        id: this.id(),
        op: this.pick(CMP),
        l: this.genExpr(1, defined),
        r: this.genExpr(1, defined),
      };
    }
    return this.genExpr(1, defined);
  }

  // 生成语句块；defined 为“当前必然已定义”的变量集合，会被就地更新。
  // 返回语句列表；块内可能以 return 结束（由调用处决定是否允许）。
  private genBlock(depth: number, defined: Set<string>, allowReturn: boolean): Stmt[] {
    const stmts: Stmt[] = [];
    const n = 1 + Math.floor(this.rnd() * 4);
    for (let i = 0; i < n; i++) {
      if (depth > 0 && this.chance(0.3)) {
        // if 语句
        const cond = this.genCond([...defined]);
        const thenDefined = new Set(defined);
        const elsDefined = new Set(defined);
        const thenBody = this.genBlock(depth - 1, thenDefined, allowReturn);
        const elsBody = this.chance(0.7) ? this.genBlock(depth - 1, elsDefined, allowReturn) : [];
        stmts.push({ kind: 'if', id: this.id(), cond, then: thenBody, els: elsBody });
        // 只有两个分支都定义的变量，汇合后才必然已定义
        const joined = new Set<string>();
        for (const v of thenDefined) if (elsDefined.has(v)) joined.add(v);
        defined.clear();
        for (const v of joined) defined.add(v);
      } else if (allowReturn && this.chance(0.1)) {
        // 块中间的 return：其后的语句是死代码，留给优化器删除
        stmts.push({ kind: 'return', id: this.id(), e: this.genExpr(2, [...defined]) });
        // return 之后仍可继续生成（死代码），但变量环境已不可靠，只用入参前的 defined
      } else {
        const name = `v${this.varCounter++}`;
        const e = this.genExpr(2 + (this.chance(0.3) ? 1 : 0), [...defined]);
        stmts.push({ kind: 'assign', id: this.id(), name, e });
        defined.add(name);
      }
    }
    return stmts;
  }

  generate(): Generated {
    // 2~4 个输入变量，取边界倾向的值
    const inputNames: string[] = [];
    const inputs: Record<string, number> = {};
    const nInputs = 2 + Math.floor(this.rnd() * 3);
    for (let i = 0; i < nInputs; i++) {
      const name = `x${i}`;
      inputNames.push(name);
      inputs[name] = this.interestingConst();
    }
    const defined = new Set(inputNames);
    const body = this.genBlock(2, defined, true);
    // 顶层保证以 return 结束
    body.push({ kind: 'return', id: this.id(), e: this.genExpr(2, [...defined]) });
    return { prog: { body, inputs: inputNames }, inputs };
  }
}

export function genProgram(seed: number): Generated {
  return new Gen(seed).generate();
}
