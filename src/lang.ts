// Instruction set for the small language.
//
// A program is a single straight-line block over integer registers r0..r{n-1}
// and a fixed-size memory array mem[0..memSize-1]. All arithmetic is 32-bit
// signed with wraparound. Observable semantics = (return value, write trace,
// first trap).

export type Reg = number;

export type BinOp =
  | "add" | "sub" | "mul"
  | "and" | "or" | "xor"
  | "lt" | "le" | "eq" | "ne";

export const BIN_OPS: BinOp[] = [
  "add", "sub", "mul", "and", "or", "xor", "lt", "le", "eq", "ne",
];

export type Instr =
  | { op: "const"; dst: Reg; value: number }          // pure
  | { op: "mov"; dst: Reg; src: Reg }                 // pure (only produced by the optimizer)
  | { op: "bin"; dst: Reg; bop: BinOp; a: Reg; b: Reg } // pure, never traps
  | { op: "div"; dst: Reg; a: Reg; b: Reg }           // traps div-by-zero when b == 0
  | { op: "mod"; dst: Reg; a: Reg; b: Reg }           // traps div-by-zero when b == 0
  | { op: "load"; dst: Reg; addr: Reg }               // traps oob-load
  | { op: "store"; addr: Reg; src: Reg }              // traps oob-store; observable write
  | { op: "assert"; cond: Reg }                       // traps assert-failed when cond == 0
  | { op: "ret"; src: Reg };                          // return regs[src]

export interface Prog {
  numRegs: number;    // registers are r0 .. r{numRegs-1}
  numInputs: number;  // r0 .. r{numInputs-1} are initialized from the input
  memSize: number;    // memory is mem[0 .. memSize-1], initialized from the input
  body: Instr[];
}

export interface Input {
  regs: number[]; // length numInputs
  mem: number[];  // length memSize
}

export type TrapKind = "div-by-zero" | "oob-load" | "oob-store" | "assert-failed";

export interface Write {
  addr: number;
  value: number;
}

export type Outcome =
  | { kind: "return"; value: number; writes: Write[] }
  | { kind: "throw"; error: TrapKind; writes: Write[] };

// Pure binary op, 32-bit wraparound semantics. Never traps.
export function evalBin(bop: BinOp, a: number, b: number): number {
  switch (bop) {
    case "add": return (a + b) | 0;
    case "sub": return (a - b) | 0;
    case "mul": return Math.imul(a, b);
    case "and": return a & b;
    case "or": return a | b;
    case "xor": return a ^ b;
    case "lt": return a < b ? 1 : 0;
    case "le": return a <= b ? 1 : 0;
    case "eq": return a === b ? 1 : 0;
    case "ne": return a !== b ? 1 : 0;
  }
}

// Truncating division/modulo. Precondition: b !== 0 (caller checks the trap).
// (INT_MIN / -1) wraps to INT_MIN instead of trapping, matching (a/b)|0.
export function evalDiv(a: number, b: number): number {
  return (a / b) | 0;
}

export function evalMod(a: number, b: number): number {
  return (a % b) | 0;
}

export function fmtInstr(ins: Instr): string {
  switch (ins.op) {
    case "const": return `r${ins.dst} = const ${ins.value}`;
    case "mov": return `r${ins.dst} = mov r${ins.src}`;
    case "bin": return `r${ins.dst} = ${ins.bop} r${ins.a}, r${ins.b}`;
    case "div": return `r${ins.dst} = div r${ins.a}, r${ins.b}`;
    case "mod": return `r${ins.dst} = mod r${ins.a}, r${ins.b}`;
    case "load": return `r${ins.dst} = load [r${ins.addr}]`;
    case "store": return `store [r${ins.addr}], r${ins.src}`;
    case "assert": return `assert r${ins.cond}`;
    case "ret": return `ret r${ins.src}`;
  }
}

export function fmtProg(p: Prog): string {
  const header = `# regs=${p.numRegs} inputs=${p.numInputs} mem=${p.memSize}`;
  return [header, ...p.body.map((ins, i) => `${i}: ${fmtInstr(ins)}`)].join("\n");
}
