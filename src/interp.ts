import type { Input, Outcome, Prog, Write } from "./lang.ts";
import { evalBin, evalDiv, evalMod } from "./lang.ts";

// Run a program to completion (straight-line: at most body.length steps).
// Returns the full observable behavior: return value OR first trap, plus the
// ordered trace of memory writes performed before termination.
export function run(p: Prog, input: Input): Outcome {
  const regs = new Array<number>(p.numRegs).fill(0);
  for (let i = 0; i < p.numInputs && i < input.regs.length; i++) {
    regs[i] = input.regs[i] | 0;
  }
  const mem = new Array<number>(p.memSize).fill(0);
  for (let i = 0; i < p.memSize && i < input.mem.length; i++) {
    mem[i] = input.mem[i] | 0;
  }
  const writes: Write[] = [];

  let pc = 0;
  while (pc < p.body.length) {
    const ins = p.body[pc];
    switch (ins.op) {
      case "const":
        regs[ins.dst] = ins.value | 0;
        break;
      case "mov":
        regs[ins.dst] = regs[ins.src];
        break;
      case "bin":
        regs[ins.dst] = evalBin(ins.bop, regs[ins.a], regs[ins.b]);
        break;
      case "div": {
        const b = regs[ins.b];
        if (b === 0) return { kind: "throw", error: "div-by-zero", writes };
        regs[ins.dst] = evalDiv(regs[ins.a], b);
        break;
      }
      case "mod": {
        const b = regs[ins.b];
        if (b === 0) return { kind: "throw", error: "div-by-zero", writes };
        regs[ins.dst] = evalMod(regs[ins.a], b);
        break;
      }
      case "load": {
        const a = regs[ins.addr];
        if (a < 0 || a >= p.memSize) return { kind: "throw", error: "oob-load", writes };
        regs[ins.dst] = mem[a];
        break;
      }
      case "store": {
        const a = regs[ins.addr];
        if (a < 0 || a >= p.memSize) return { kind: "throw", error: "oob-store", writes };
        mem[a] = regs[ins.src];
        writes.push({ addr: a, value: regs[ins.src] });
        break;
      }
      case "assert":
        if (regs[ins.cond] === 0) return { kind: "throw", error: "assert-failed", writes };
        break;
      case "ret":
        return { kind: "return", value: regs[ins.src], writes };
    }
    pc++;
  }
  // Falling off the end is an implicit `ret r0`.
  return { kind: "return", value: regs[0], writes };
}
