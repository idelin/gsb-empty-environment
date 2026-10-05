// 8 位有符号整数小语言的 AST 定义与构造器。
//
// 语言语义：
// - 所有值都是 8 位有符号整数（-128..127），算术溢出按二进制补码回绕；
// - 条件以非零为真；比较与逻辑运算产生 0/1；
// - 读取未赋值的变量得到 0；
// - while 循环为有界循环：循环体最多执行 Program.loopBound 次，
//   超过上界仍未退出的路径记为“越界”并丢弃（不参与后续断言判定）。

export type ArithOp = "add" | "sub" | "mul" | "and" | "or" | "xor";
export type CmpOp = "eq" | "ne" | "lt" | "le" | "gt" | "ge";
export type BinOp = ArithOp | CmpOp;

export type Expr =
  | { kind: "num"; id: number; value: number }
  | { kind: "var"; id: number; name: string }
  | { kind: "input"; id: number; name: string }
  | { kind: "bin"; id: number; op: BinOp; l: Expr; r: Expr }
  | { kind: "land"; id: number; l: Expr; r: Expr }
  | { kind: "lor"; id: number; l: Expr; r: Expr }
  | { kind: "lnot"; id: number; e: Expr }
  | { kind: "neg"; id: number; e: Expr };

export type Stmt =
  | { kind: "assign"; id: number; name: string; expr: Expr }
  | { kind: "if"; id: number; cond: Expr; then: Stmt[]; els: Stmt[] }
  | { kind: "while"; id: number; cond: Expr; body: Stmt[] }
  | { kind: "assert"; id: number; cond: Expr; msg: string };

export interface Program {
  inputs: string[];
  body: Stmt[];
  loopBound: number;
}

// 每个 AST 节点带稳定 id，用于把符号分析结果（溢出点、分支）与具体执行对照。
let nextId = 1;
export function resetIds(): void {
  nextId = 1;
}
function nid(): number {
  return nextId++;
}

export function wrap8(v: number): number {
  const b = ((v % 256) + 256) % 256;
  return b >= 128 ? b - 256 : b;
}

export function toByte(v: number): number {
  return ((v % 256) + 256) % 256;
}

export function num(value: number): Expr {
  return { kind: "num", id: nid(), value: wrap8(value) };
}
export function v(name: string): Expr {
  return { kind: "var", id: nid(), name };
}
export function inp(name: string): Expr {
  return { kind: "input", id: nid(), name };
}
export function bin(op: BinOp, l: Expr, r: Expr): Expr {
  return { kind: "bin", id: nid(), op, l, r };
}
export const add = (l: Expr, r: Expr): Expr => bin("add", l, r);
export const sub = (l: Expr, r: Expr): Expr => bin("sub", l, r);
export const mul = (l: Expr, r: Expr): Expr => bin("mul", l, r);
export const land = (l: Expr, r: Expr): Expr => ({ kind: "land", id: nid(), l, r });
export const lor = (l: Expr, r: Expr): Expr => ({ kind: "lor", id: nid(), l, r });
export const lnot = (e: Expr): Expr => ({ kind: "lnot", id: nid(), e });
export const neg = (e: Expr): Expr => ({ kind: "neg", id: nid(), e });

export function assign(name: string, expr: Expr): Stmt {
  return { kind: "assign", id: nid(), name, expr };
}
export function iff(cond: Expr, then: Stmt[], els: Stmt[] = []): Stmt {
  return { kind: "if", id: nid(), cond, then, els };
}
export function whileDo(cond: Expr, body: Stmt[]): Stmt {
  return { kind: "while", id: nid(), cond, body };
}
export function assertThat(cond: Expr, msg = ""): Stmt {
  return { kind: "assert", id: nid(), cond, msg };
}
export function program(inputs: string[], body: Stmt[], loopBound = 16): Program {
  return { inputs, body, loopBound };
}
