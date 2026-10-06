// 整数中间码（IR）定义：32 位有符号整数、带分支。
// 每个节点携带稳定 id，作为“执行位置”，优化前后 id 不变，便于比对错误轨迹。

export type BinOp =
  | 'add' | 'sub' | 'mul' | 'div' | 'mod'
  | 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Expr =
  | { kind: 'const'; id: number; value: number }
  | { kind: 'var'; id: number; name: string }
  | { kind: 'neg'; id: number; e: Expr }
  | { kind: 'bin'; id: number; op: BinOp; l: Expr; r: Expr };

export type Stmt =
  | { kind: 'assign'; id: number; name: string; e: Expr }
  | { kind: 'if'; id: number; cond: Expr; then: Stmt[]; els: Stmt[] }
  | { kind: 'return'; id: number; e: Expr };

export interface Program {
  body: Stmt[];
  inputs: string[]; // 入口时已绑定的变量
}

export const I32_MIN = -2147483648;
export const I32_MAX = 2147483647;

export function isI32(v: number): boolean {
  return Number.isInteger(v) && v >= I32_MIN && v <= I32_MAX;
}
