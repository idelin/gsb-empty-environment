// IR for a small straight-line language.
// Temps are SSA: each instruction writes a fresh temp, temps are never redefined.

export type Temp = number;

export type PureOp = 'add' | 'sub' | 'mul' | 'and' | 'or' | 'xor' | 'shl' | 'shr';
export type DivOp = 'div' | 'mod';

export type Instr =
  | { kind: 'const'; dst: Temp; value: number }
  | { kind: 'input'; dst: Temp; index: number } // read inputs[index]
  | { kind: 'binop'; dst: Temp; op: PureOp; a: Temp; b: Temp } // pure, never throws
  | { kind: 'divmod'; dst: Temp; op: DivOp; a: Temp; b: Temp } // throws on zero divisor
  | { kind: 'load'; dst: Temp; addr: Temp } // throws on out-of-bounds address
  | { kind: 'store'; addr: Temp; value: Temp } // throws on out-of-bounds address
  | { kind: 'ret'; value: Temp };

export interface Program {
  instrs: Instr[];
}

export const COMMUTATIVE: ReadonlySet<PureOp> = new Set(['add', 'mul', 'and', 'or', 'xor']);

export function fmtTemp(t: Temp): string {
  return `t${t}`;
}

export function fmtInstr(ins: Instr): string {
  switch (ins.kind) {
    case 'const':
      return `${fmtTemp(ins.dst)} = const ${ins.value}`;
    case 'input':
      return `${fmtTemp(ins.dst)} = input ${ins.index}`;
    case 'binop':
      return `${fmtTemp(ins.dst)} = ${ins.op} ${fmtTemp(ins.a)}, ${fmtTemp(ins.b)}`;
    case 'divmod':
      return `${fmtTemp(ins.dst)} = ${ins.op} ${fmtTemp(ins.a)}, ${fmtTemp(ins.b)}`;
    case 'load':
      return `${fmtTemp(ins.dst)} = load ${fmtTemp(ins.addr)}`;
    case 'store':
      return `store ${fmtTemp(ins.addr)}, ${fmtTemp(ins.value)}`;
    case 'ret':
      return `ret ${fmtTemp(ins.value)}`;
  }
}

export function fmtProgram(p: Program): string {
  return p.instrs.map((ins, i) => `${String(i).padStart(3)}  ${fmtInstr(ins)}`).join('\n');
}
