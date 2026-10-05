// 符号值与求解器。
// 符号值就是表达式树(叶子为 input/const),路径条件为布尔表达式树。
// 由于输入域有限(每个输入 256 个取值),求解器按字典序枚举给出最小模型,
// 这本身就是该有限域上的完备判定过程。

import {
  INT8_MIN, INT8_MAX, toInt8, arithRaw, arithOverflows, applyCmp,
  C, and, or, neg, not, bin, cmp, truthy,
  type Expr,
} from "./lang.ts";

// 在具体输入赋值下求值符号表达式(不允许出现 var 节点)
export function evalModel(e: Expr, model: number[]): number {
  switch (e.kind) {
    case "const": return e.value;
    case "input": return toInt8(model[e.index] ?? 0);
    case "var": throw new Error("路径条件中不应出现 var 节点");
    case "neg": return toInt8(-evalModel(e.e, model));
    case "not": return evalModel(e.e, model) === 0 ? 1 : 0;
    case "bin": return toInt8(arithRaw(e.op, evalModel(e.l, model), evalModel(e.r, model)));
    case "cmp": return applyCmp(e.op, evalModel(e.l, model), evalModel(e.r, model)) ? 1 : 0;
    case "logic": {
      const l = evalModel(e.l, model) !== 0;
      const r = evalModel(e.r, model) !== 0;
      return (e.op === "&&" ? l && r : l || r) ? 1 : 0;
    }
    case "ovf": {
      const l = evalModel(e.l, model);
      const r = e.r === null ? 0 : evalModel(e.r, model);
      return arithOverflows(e.op, l, r) ? 1 : 0;
    }
  }
}

export function collectInputs(e: Expr, into: Set<number> = new Set()): Set<number> {
  switch (e.kind) {
    case "input": into.add(e.index); break;
    case "neg": case "not": collectInputs(e.e, into); break;
    case "bin": case "cmp": case "logic": collectInputs(e.l, into); collectInputs(e.r, into); break;
    case "ovf": collectInputs(e.l, into); if (e.r) collectInputs(e.r, into); break;
    default: break;
  }
  return into;
}

export class Solver {
  private cache = new Map<string, number[] | null>();
  calls = 0;
  cacheHits = 0;

  readonly numInputs: number;

  constructor(numInputs: number) {
    this.numInputs = numInputs;
  }

  // 按 (in0, in1, ...) 字典序求最小模型;未出现的输入取最小值 INT8_MIN。
  solveMin(cond: Expr): number[] | null {
    const key = JSON.stringify(cond);
    const hit = this.cache.get(key);
    if (hit !== undefined) {
      this.cacheHits++;
      return hit === null ? null : [...hit];
    }
    this.calls++;
    const used = [...collectInputs(cond)].sort((a, b) => a - b);
    const model = new Array<number>(this.numInputs).fill(INT8_MIN);
    let found: number[] | null = null;
    const rec = (i: number): boolean => {
      if (i === used.length) return evalModel(cond, model) !== 0;
      const idx = used[i];
      for (let v = INT8_MIN; v <= INT8_MAX; v++) {
        model[idx] = v;
        if (rec(i + 1)) return true;
      }
      model[idx] = INT8_MIN;
      return false;
    };
    if (rec(0)) found = [...model];
    this.cache.set(key, found);
    return found === null ? null : [...found];
  }

  satisfiable(cond: Expr): boolean {
    return this.solveMin(cond) !== null;
  }
}

// ---------- 符号求值 ----------

export type SymStore = Map<string, Expr>;

export type SymValue = { val: Expr; ovf: Expr };

const noOvf = (): Expr => C(0);

function ovfNode(op: "neg" | "+" | "-" | "*", l: Expr, r: Expr | null): Expr {
  const lc = l.kind === "const" ? l : null;
  const rc = r !== null && r.kind === "const" ? r : null;
  if (lc && (r === null || rc)) {
    return C(arithOverflows(op, lc.value, rc ? rc.value : 0) ? 1 : 0);
  }
  return { kind: "ovf", op, l, r };
}

// 在符号存储下求值程序表达式,返回符号值与“本次求值发生溢出”的条件
export function evalSym(e: Expr, store: SymStore): SymValue {
  switch (e.kind) {
    case "const":
    case "input":
      return { val: e, ovf: noOvf() };
    case "var":
      return { val: store.get(e.name) ?? C(0), ovf: noOvf() };
    case "neg": {
      const s = evalSym(e.e, store);
      return { val: neg(s.val), ovf: or(s.ovf, ovfNode("neg", s.val, null)) };
    }
    case "not": {
      const s = evalSym(e.e, store);
      return { val: not(truthy(s.val)), ovf: s.ovf };
    }
    case "bin": {
      const l = evalSym(e.l, store);
      const r = evalSym(e.r, store);
      return {
        val: bin(e.op, l.val, r.val),
        ovf: or(or(l.ovf, r.ovf), ovfNode(e.op, l.val, r.val)),
      };
    }
    case "cmp": {
      const l = evalSym(e.l, store);
      const r = evalSym(e.r, store);
      return { val: cmp(e.op, l.val, r.val), ovf: or(l.ovf, r.ovf) };
    }
    case "logic": {
      const l = evalSym(e.l, store);
      const r = evalSym(e.r, store);
      const val = e.op === "&&" ? and(truthy(l.val), truthy(r.val)) : or(truthy(l.val), truthy(r.val));
      return { val, ovf: or(l.ovf, r.ovf) };
    }
    case "ovf":
      throw new Error("ovf 节点只允许出现在路径条件中");
  }
}
