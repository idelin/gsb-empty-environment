/**
 * 整数中间码（IR）：32 位有符号整数，溢出与除零是可见错误。
 * 每条指令有稳定 id，优化重写指令时保留 id，
 * 因此错误轨迹（执行过的指令 id 序列 + 出错位置）可跨版本比较。
 */

export const INT32_MIN = -(2n ** 31n);
export const INT32_MAX = 2n ** 31n - 1n;

export type BinOp = 'add' | 'sub' | 'mul' | 'div' | 'mod';

export type Operand =
  | { kind: 'reg'; reg: number }
  | { kind: 'imm'; value: number };

export type Instr =
  | { id: number; op: 'const'; dst: number; value: number; label?: string }
  | { id: number; op: 'input'; dst: number; label?: string }
  | { id: number; op: BinOp; dst: number; a: Operand; b: Operand; label?: string }
  | { id: number; op: 'br'; cond: Operand; thenLabel: string; elseLabel: string; label?: string }
  | { id: number; op: 'jmp'; target: string; label?: string }
  | { id: number; op: 'ret'; src: Operand; label?: string };

export interface Program {
  instrs: Instr[];
}

export type ErrorKind = 'overflow' | 'div-by-zero';

export class EvalError extends Error {
  constructor(public readonly kind: ErrorKind, message: string) {
    super(message);
    this.name = 'EvalError';
  }
}

export function inRange(v: bigint): boolean {
  return v >= INT32_MIN && v <= INT32_MAX;
}

function checked(v: bigint, op: BinOp): bigint {
  if (!inRange(v)) {
    throw new EvalError('overflow', `${op} result ${v} out of int32 range`);
  }
  return v;
}

/** 带检查的 32 位整数运算；溢出 / 除零抛 EvalError。 */
export function evalBinOp(op: BinOp, a: bigint, b: bigint): bigint {
  switch (op) {
    case 'add':
      return checked(a + b, op);
    case 'sub':
      return checked(a - b, op);
    case 'mul':
      return checked(a * b, op);
    case 'div':
      if (b === 0n) throw new EvalError('div-by-zero', 'division by zero');
      // INT32_MIN / -1 在 int32 下溢出
      return checked(a / b, op);
    case 'mod':
      if (b === 0n) throw new EvalError('div-by-zero', 'modulo by zero');
      // INT32_MIN % -1 数学结果为 0，不视为溢出
      return a % b;
  }
}

export function formatInstr(ins: Instr): string {
  const lbl = ins.label ? `${ins.label}: ` : '';
  const fmtOp = (o: Operand) => (o.kind === 'reg' ? `r${o.reg}` : `${o.value}`);
  switch (ins.op) {
    case 'const':
      return `${lbl}#${ins.id} r${ins.dst} = const ${ins.value}`;
    case 'input':
      return `${lbl}#${ins.id} r${ins.dst} = input`;
    case 'br':
      return `${lbl}#${ins.id} br ${fmtOp(ins.cond)}, ${ins.thenLabel}, ${ins.elseLabel}`;
    case 'jmp':
      return `${lbl}#${ins.id} jmp ${ins.target}`;
    case 'ret':
      return `${lbl}#${ins.id} ret ${fmtOp(ins.src)}`;
    default:
      return `${lbl}#${ins.id} r${ins.dst} = ${ins.op} ${fmtOp(ins.a)}, ${fmtOp(ins.b)}`;
  }
}

export function formatProgram(p: Program): string {
  return p.instrs.map(formatInstr).join('\n');
}
