export const enum Op {
  Push = 0x01,
  Pop = 0x02,
  Add = 0x03,
  Jmp = 0x04,
  Jz = 0x05,
  Ret = 0x06,
}

export interface Instr {
  op: Op;
  offset: number;
  size: number;
  target?: number;
}

export type DecodeResult =
  | { ok: true; instrs: Instr[]; byOffset: Map<number, Instr>; boundaries: Set<number> }
  | { ok: false; at: number; message: string };

export function decode(code: Uint8Array): DecodeResult {
  const instrs: Instr[] = [];
  let pc = 0;
  while (pc < code.length) {
    const op = code[pc] as Op;
    switch (op) {
      case Op.Push:
      case Op.Pop:
      case Op.Add:
      case Op.Ret:
        instrs.push({ op, offset: pc, size: 1 });
        pc += 1;
        break;
      case Op.Jmp:
      case Op.Jz: {
        if (pc + 2 >= code.length) {
          return { ok: false, at: pc, message: "truncated jump operand" };
        }
        const target = code[pc + 1] | (code[pc + 2] << 8);
        instrs.push({ op, offset: pc, size: 3, target });
        pc += 3;
        break;
      }
      default:
        return { ok: false, at: pc, message: `unknown opcode 0x${(op as number).toString(16)}` };
    }
  }
  const byOffset = new Map<number, Instr>();
  const boundaries = new Set<number>();
  for (const instr of instrs) {
    byOffset.set(instr.offset, instr);
    boundaries.add(instr.offset);
  }
  return { ok: true, instrs, byOffset, boundaries };
}

export interface InstrSpec {
  op: Op;
  targetIndex?: number;
  rawTarget?: number;
}

export function encodeProgram(specs: InstrSpec[]): Uint8Array {
  const offsets: number[] = [];
  let end = 0;
  for (const spec of specs) {
    offsets.push(end);
    end += spec.op === Op.Jmp || spec.op === Op.Jz ? 3 : 1;
  }
  const bytes: number[] = [];
  specs.forEach((spec, i) => {
    bytes.push(spec.op);
    if (spec.op === Op.Jmp || spec.op === Op.Jz) {
      let target: number;
      if (spec.rawTarget !== undefined) {
        target = spec.rawTarget;
      } else {
        const idx = spec.targetIndex ?? 0;
        target = idx >= 0 && idx < offsets.length ? offsets[idx] : end;
      }
      bytes.push(target & 0xff, (target >> 8) & 0xff);
    }
  });
  return new Uint8Array(bytes);
}

export function disasm(instr: Instr): string {
  switch (instr.op) {
    case Op.Push:
      return "PUSH";
    case Op.Pop:
      return "POP";
    case Op.Add:
      return "ADD";
    case Op.Jmp:
      return `JMP ${instr.target}`;
    case Op.Jz:
      return `JZ ${instr.target}`;
    case Op.Ret:
      return "RET";
  }
}
