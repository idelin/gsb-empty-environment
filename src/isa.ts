// Instruction set for a tiny stack VM. Instructions are variable-length,
// so "jump target must land on an instruction boundary" is meaningful.

export const Op = {
  PUSH: 0x01, // [PUSH, u8]  push constant
  POP: 0x02,  // pop 1
  DUP: 0x03,  // duplicate top of stack
  ADD: 0x04,  // pop 2, push sum
  JMP: 0x05,  // [JMP, u8]  unconditional absolute jump
  JZ: 0x06,   // [JZ, u8]   pop 1; jump if zero, else fall through
  RET: 0x07,  // return; stack height must be exactly 1
} as const;

export type OpCode = (typeof Op)[keyof typeof Op];

export const OP_NAMES: Readonly<Record<number, string>> = {
  [Op.PUSH]: "PUSH",
  [Op.POP]: "POP",
  [Op.DUP]: "DUP",
  [Op.ADD]: "ADD",
  [Op.JMP]: "JMP",
  [Op.JZ]: "JZ",
  [Op.RET]: "RET",
};

export const OP_SIZES: Readonly<Record<number, number>> = {
  [Op.PUSH]: 2,
  [Op.POP]: 1,
  [Op.DUP]: 1,
  [Op.ADD]: 1,
  [Op.JMP]: 2,
  [Op.JZ]: 2,
  [Op.RET]: 1,
};

export interface Instr {
  offset: number;
  op: OpCode;
  arg: number | null;
  size: number;
}

export type DecodeResult =
  | { ok: true; instrs: Instr[]; byOffset: Map<number, Instr> }
  | { ok: false; offset: number; error: string };

export function decode(code: Uint8Array): DecodeResult {
  const instrs: Instr[] = [];
  const byOffset = new Map<number, Instr>();
  let offset = 0;
  while (offset < code.length) {
    const op = code[offset] as OpCode;
    const size = OP_SIZES[op];
    if (size === undefined) {
      return { ok: false, offset, error: `unknown opcode 0x${code[offset].toString(16)}` };
    }
    if (offset + size > code.length) {
      return { ok: false, offset, error: `truncated ${OP_NAMES[op]}` };
    }
    const instr: Instr = { offset, op, arg: size === 2 ? code[offset + 1] : null, size };
    instrs.push(instr);
    byOffset.set(offset, instr);
    offset += size;
  }
  return { ok: true, instrs, byOffset };
}

export function disassemble(instr: Instr): string {
  const name = OP_NAMES[instr.op];
  return instr.arg === null ? name : `${name} ${instr.arg}`;
}

// Minimal two-pass assembler with labels, for readable tests/examples.
export type Line =
  | ["label", string]
  | ["push", number]
  | ["pop"]
  | ["dup"]
  | ["add"]
  | ["ret"]
  | ["jmp", string]
  | ["jz", string];

export function assemble(lines: Line[]): Uint8Array {
  const labels = new Map<string, number>();
  let offset = 0;
  for (const line of lines) {
    if (line[0] === "label") {
      labels.set(line[1], offset);
    } else {
      offset += line[0] === "push" || line[0] === "jmp" || line[0] === "jz" ? 2 : 1;
    }
  }
  const bytes: number[] = [];
  for (const line of lines) {
    switch (line[0]) {
      case "label":
        break;
      case "push":
        bytes.push(Op.PUSH, line[1] & 0xff);
        break;
      case "pop":
        bytes.push(Op.POP);
        break;
      case "dup":
        bytes.push(Op.DUP);
        break;
      case "add":
        bytes.push(Op.ADD);
        break;
      case "ret":
        bytes.push(Op.RET);
        break;
      case "jmp":
      case "jz": {
        const target = labels.get(line[1]);
        if (target === undefined) throw new Error(`undefined label: ${line[1]}`);
        bytes.push(line[0] === "jmp" ? Op.JMP : Op.JZ, target);
        break;
      }
    }
  }
  return Uint8Array.from(bytes);
}
