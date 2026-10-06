import { Instr, Operand, Program, EvalError, ErrorKind, evalBinOp } from './ir';

export type Outcome =
  | { ok: true; value: bigint }
  | { ok: false; error: ErrorKind; instrId: number };

export interface RunResult {
  /** 按执行顺序记录的指令 id（错误轨迹） */
  trace: number[];
  outcome: Outcome;
}

const STEP_LIMIT = 100_000;

/**
 * 解释执行：记录每条执行指令的 id；出错时记录错误类别与出错指令 id。
 * input 指令按顺序从 inputs 取值；两个解释器必须使用同一输入序列。
 */
export function run(program: Program, inputs: number[] = []): RunResult {
  const labelToIndex = new Map<string, number>();
  program.instrs.forEach((ins, i) => {
    if (ins.label !== undefined) labelToIndex.set(ins.label, i);
  });

  const regs = new Map<number, bigint>();
  const trace: number[] = [];
  let pc = 0;
  let steps = 0;
  let inputIndex = 0;

  const read = (o: Operand): bigint => {
    if (o.kind === 'imm') return BigInt(o.value);
    const v = regs.get(o.reg);
    if (v === undefined) throw new Error(`internal: read of undefined register r${o.reg}`);
    return v;
  };

  const jumpTo = (label: string): number => {
    const idx = labelToIndex.get(label);
    if (idx === undefined) throw new Error(`internal: unknown label ${label}`);
    return idx;
  };

  while (pc < program.instrs.length) {
    if (++steps > STEP_LIMIT) throw new Error('internal: step limit exceeded');
    const ins: Instr = program.instrs[pc]!;
    trace.push(ins.id);
    try {
      switch (ins.op) {
        case 'const':
          regs.set(ins.dst, BigInt(ins.value));
          pc++;
          break;
        case 'input': {
          if (inputIndex >= inputs.length) {
            throw new Error('internal: not enough inputs provided');
          }
          regs.set(ins.dst, BigInt(inputs[inputIndex++]!));
          pc++;
          break;
        }
        case 'add':
        case 'sub':
        case 'mul':
        case 'div':
        case 'mod':
          regs.set(ins.dst, evalBinOp(ins.op, read(ins.a), read(ins.b)));
          pc++;
          break;
        case 'br':
          pc = read(ins.cond) !== 0n ? jumpTo(ins.thenLabel) : jumpTo(ins.elseLabel);
          break;
        case 'jmp':
          pc = jumpTo(ins.target);
          break;
        case 'ret':
          return { trace, outcome: { ok: true, value: read(ins.src) } };
      }
    } catch (e) {
      if (e instanceof EvalError) {
        return { trace, outcome: { ok: false, error: e.kind, instrId: ins.id } };
      }
      throw e;
    }
  }
  throw new Error('internal: program fell off the end without ret');
}
