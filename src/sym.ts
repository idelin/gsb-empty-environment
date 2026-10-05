// 符号值：以输入变量为叶子的表达式 DAG。
// 布尔量用 0/1 的 8 位值表示（非零为真），与语言语义一致。
// ite(c, t, e) 用于在合并点时按路径条件选择不同路径上的值，
// 从而“状态合并但保留各自路径条件”。

import { wrap8 } from "./ast.ts";
import type { ArithOp, CmpOp } from "./ast.ts";

export type Sym =
  | { k: "c"; v: number }
  | { k: "in"; name: string }
  | { k: "bin"; op: ArithOp; l: Sym; r: Sym }
  | { k: "cmp"; op: CmpOp; l: Sym; r: Sym }
  | { k: "ite"; c: Sym; t: Sym; e: Sym }
  | { k: "not"; x: Sym }
  // ovf: 算术溢出谓词（不回绕，直接按数学结果判断是否落在 [-128,127] 外），值为 0/1
  | { k: "ovf"; op: "add" | "sub" | "mul"; l: Sym; r: Sym };

export interface Env {
  [name: string]: number;
}

export function arith(op: ArithOp, a: number, b: number): number {
  switch (op) {
    case "add": return a + b;
    case "sub": return a - b;
    case "mul": return a * b;
    case "and": return a & b;
    case "or": return a | b;
    case "xor": return a ^ b;
  }
}

export function cmpOp(op: CmpOp, a: number, b: number): boolean {
  switch (op) {
    case "eq": return a === b;
    case "ne": return a !== b;
    case "lt": return a < b;
    case "le": return a <= b;
    case "gt": return a > b;
    case "ge": return a >= b;
  }
}

export function overflows(op: "add" | "sub" | "mul", a: number, b: number): boolean {
  const r = arith(op, a, b);
  return r < -128 || r > 127;
}

export const C0: Sym = { k: "c", v: 0 };
export const C1: Sym = { k: "c", v: 1 };

export const sc = (v: number): Sym => ({ k: "c", v: wrap8(v) });
export const sIn = (name: string): Sym => ({ k: "in", name });

const isC = (s: Sym): s is { k: "c"; v: number } => s.k === "c";

export function sBin(op: ArithOp, l: Sym, r: Sym): Sym {
  if (isC(l) && isC(r)) return sc(arith(op, l.v, r.v));
  return { k: "bin", op, l, r };
}

export function sCmp(op: CmpOp, l: Sym, r: Sym): Sym {
  if (isC(l) && isC(r)) return cmpOp(op, l.v, r.v) ? C1 : C0;
  return { k: "cmp", op, l, r };
}

// 结构上已保证值为 0/1 的表达式
function isBoolSym(s: Sym): boolean {
  if (isC(s)) return true;
  switch (s.k) {
    case "cmp":
    case "not":
    case "ovf":
      return true;
    case "bin":
      return (
        (s.op === "and" || s.op === "or" || s.op === "xor") &&
        isBoolSym(s.l) &&
        isBoolSym(s.r)
      );
    default:
      return false;
  }
}

// sBool(x) = (x != 0) ? 1 : 0
export function sBool(x: Sym): Sym {
  if (isC(x)) return x.v !== 0 ? C1 : C0;
  if (isBoolSym(x)) return x;
  return sCmp("ne", x, C0);
}

export function sNot(x: Sym): Sym {
  if (isC(x)) return x.v === 0 ? C1 : C0;
  if (x.k === "not") return sBool(x.x);
  return { k: "not", x };
}

export function sAnd(a: Sym, b: Sym): Sym {
  const ba = sBool(a);
  const bb = sBool(b);
  if (isC(ba)) return ba.v !== 0 ? bb : C0;
  if (isC(bb)) return bb.v !== 0 ? ba : C0;
  return { k: "bin", op: "and", l: ba, r: bb };
}

export function sOr(a: Sym, b: Sym): Sym {
  const ba = sBool(a);
  const bb = sBool(b);
  if (isC(ba)) return ba.v !== 0 ? C1 : bb;
  if (isC(bb)) return bb.v !== 0 ? C1 : ba;
  return { k: "bin", op: "or", l: ba, r: bb };
}

export function sAndAll(xs: Sym[]): Sym {
  let acc: Sym = C1;
  for (const x of xs) {
    if (isC(x) && x.v === 0) return C0;
    acc = sAnd(acc, x);
  }
  return acc;
}

export function sOrAll(xs: Sym[]): Sym {
  let acc: Sym = C0;
  for (const x of xs) acc = sOr(acc, x);
  return acc;
}

export function sIte(c: Sym, t: Sym, e: Sym): Sym {
  if (isC(c)) return c.v !== 0 ? t : e;
  if (t === e) return t;
  return { k: "ite", c: sBool(c), t, e };
}

export function sOvf(op: "add" | "sub" | "mul", l: Sym, r: Sym): Sym {
  if (isC(l) && isC(r)) return overflows(op, l.v, r.v) ? C1 : C0;
  return { k: "ovf", op, l, r };
}

export function evalSym(s: Sym, env: Env): number {
  switch (s.k) {
    case "c": return s.v;
    case "in": return env[s.name];
    case "bin": return wrap8(arith(s.op, evalSym(s.l, env), evalSym(s.r, env)));
    case "cmp": return cmpOp(s.op, evalSym(s.l, env), evalSym(s.r, env)) ? 1 : 0;
    case "ite": return evalSym(s.c, env) !== 0 ? evalSym(s.t, env) : evalSym(s.e, env);
    case "not": return evalSym(s.x, env) === 0 ? 1 : 0;
    case "ovf": return overflows(s.op, evalSym(s.l, env), evalSym(s.r, env)) ? 1 : 0;
  }
}

export function symSize(s: Sym): number {
  switch (s.k) {
    case "c":
    case "in":
      return 1;
    case "not":
      return 1 + symSize(s.x);
    case "ite":
      return 1 + symSize(s.c) + symSize(s.t) + symSize(s.e);
    default:
      return 1 + symSize(s.l) + symSize(s.r);
  }
}

export function symKey(s: Sym): string {
  switch (s.k) {
    case "c": return `c${s.v}`;
    case "in": return `i:${s.name}`;
    case "bin": return `b:${s.op}(${symKey(s.l)},${symKey(s.r)})`;
    case "cmp": return `m:${s.op}(${symKey(s.l)},${symKey(s.r)})`;
    case "ite": return `?(${symKey(s.c)},${symKey(s.t)},${symKey(s.e)})`;
    case "not": return `!(${symKey(s.x)})`;
    case "ovf": return `o:${s.op}(${symKey(s.l)},${symKey(s.r)})`;
  }
}

export function symEq(a: Sym, b: Sym): boolean {
  return a === b || symKey(a) === symKey(b);
}

const ARITH_SYM: Record<ArithOp, string> = {
  add: "+", sub: "-", mul: "*", and: "&", or: "|", xor: "^",
};
const CMP_SYM: Record<CmpOp, string> = {
  eq: "==", ne: "!=", lt: "<", le: "<=", gt: ">", ge: ">=",
};

export function showSym(s: Sym): string {
  switch (s.k) {
    case "c": return String(s.v);
    case "in": return s.name;
    case "bin": return `(${showSym(s.l)} ${ARITH_SYM[s.op]} ${showSym(s.r)})`;
    case "cmp": return `(${showSym(s.l)} ${CMP_SYM[s.op]} ${showSym(s.r)})`;
    case "ite": return `(ite ${showSym(s.c)} ? ${showSym(s.t)} : ${showSym(s.e)})`;
    case "not": return `!(${showSym(s.x)})`;
    case "ovf": return `ovf(${showSym(s.l)} ${ARITH_SYM[s.op]} ${showSym(s.r)})`;
  }
}
