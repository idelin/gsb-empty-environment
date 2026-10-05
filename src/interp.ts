import { Instr, Program, Temp } from './lang';

export interface MachineInput {
  inputs: number[]; // read by `input` instructions
  memory: number[]; // initial memory image; its length is the memory size
}

export interface Write {
  addr: number;
  value: number;
}

// Observable behavior of a run: the return value, the ordered trace of memory
// writes, and the first exception (if any). Two runs are equivalent iff all
// three agree.
export interface Outcome {
  ret: number | null;
  writes: Write[];
  error: string | null;
}

export class LangError extends Error {
  constructor(public readonly kind: string) {
    super(kind);
    this.name = 'LangError';
  }
}

function evalBinop(op: string, a: number, b: number): number {
  switch (op) {
    case 'add': return (a + b) | 0;
    case 'sub': return (a - b) | 0;
    case 'mul': return Math.imul(a, b);
    case 'and': return a & b;
    case 'or': return a | b;
    case 'xor': return a ^ b;
    case 'shl': return a << (b & 31);
    case 'shr': return a >> (b & 31);
    default: throw new Error(`unknown op ${op}`);
  }
}

export function run(prog: Program, input: MachineInput): Outcome {
  const temps = new Map<Temp, number>();
  const memory = input.memory.slice();
  const writes: Write[] = [];

  const get = (t: Temp): number => {
    const v = temps.get(t);
    if (v === undefined) throw new Error(`undefined temp t${t}`);
    return v;
  };

  let ret: number | null = null;
  let error: string | null = null;

  try {
    for (const ins of prog.instrs) {
      switch (ins.kind) {
        case 'const':
          temps.set(ins.dst, ins.value | 0);
          break;
        case 'input': {
          const v = input.inputs[ins.index];
          if (v === undefined) throw new LangError('InputOutOfBounds');
          temps.set(ins.dst, v | 0);
          break;
        }
        case 'binop':
          temps.set(ins.dst, evalBinop(ins.op, get(ins.a), get(ins.b)));
          break;
        case 'divmod': {
          const a = get(ins.a);
          const b = get(ins.b);
          if (b === 0) throw new LangError(ins.op === 'div' ? 'DivByZero' : 'ModByZero');
          temps.set(ins.dst, ins.op === 'div' ? Math.trunc(a / b) : a % b);
          break;
        }
        case 'load': {
          const addr = get(ins.addr);
          if (addr < 0 || addr >= memory.length) throw new LangError('LoadOutOfBounds');
          temps.set(ins.dst, memory[addr]!);
          break;
        }
        case 'store': {
          const addr = get(ins.addr);
          const value = get(ins.value);
          if (addr < 0 || addr >= memory.length) throw new LangError('StoreOutOfBounds');
          memory[addr] = value;
          writes.push({ addr, value });
          break;
        }
        case 'ret':
          ret = get(ins.value);
          return { ret, writes, error };
      }
    }
  } catch (e) {
    if (e instanceof LangError) {
      error = e.kind;
    } else {
      throw e;
    }
  }
  return { ret, writes, error };
}

export function sameOutcome(a: Outcome, b: Outcome): boolean {
  if (a.ret !== b.ret || a.error !== b.error || a.writes.length !== b.writes.length) return false;
  for (let i = 0; i < a.writes.length; i++) {
    const wa = a.writes[i]!;
    const wb = b.writes[i]!;
    if (wa.addr !== wb.addr || wa.value !== wb.value) return false;
  }
  return true;
}
