// Seeded random program generator. Programs are biased to contain redundant
// computations, aliasing load/store pairs, and trapping instructions, so the
// differential test actually exercises the optimizer's interesting paths.

import type { Instr, Prog } from "./lang.ts";
import { BIN_OPS } from "./lang.ts";

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function genProgram(seed: number): Prog {
  const rnd = mulberry32(seed);
  const numRegs = 6;
  const numInputs = 3;
  const memSize = 4;
  const ri = (): number => Math.floor(rnd() * numRegs);
  const pick = <T>(xs: readonly T[]): T => xs[Math.floor(rnd() * xs.length)];
  // Small constants, including out-of-bounds addresses (memSize=4) and
  // zero divisors, so traps happen often.
  const smallConst = (): number => pick([-2, -1, 0, 0, 1, 1, 2, 3, 4, 5]);

  const body: Instr[] = [];
  // Recently emitted computations, reused to create CSE opportunities.
  const templates: Instr[] = [];
  const remember = (ins: Instr): void => {
    templates.push(ins);
    if (templates.length > 8) templates.shift();
  };
  // Emit an address operand: often a fresh small constant (frequent
  // must-alias / provable-non-alias pairs), sometimes an arbitrary register.
  const withAddr = (emit: (addr: number) => void): void => {
    let addr: number;
    if (rnd() < 0.6) {
      addr = ri();
      body.push({ op: "const", dst: addr, value: pick([0, 1, 2, 3, 4]) });
    } else {
      addr = ri();
    }
    emit(addr);
  };

  const len = 6 + Math.floor(rnd() * 16);
  for (let i = 0; i < len; i++) {
    if (rnd() < 0.15 && templates.length > 0) {
      // Deliberate duplicate of an earlier computation.
      const t = pick(templates);
      body.push({ ...t, dst: ri() });
      continue;
    }
    const kind = rnd();
    if (kind < 0.18) {
      body.push({ op: "const", dst: ri(), value: smallConst() });
    } else if (kind < 0.46) {
      const ins: Instr = { op: "bin", dst: ri(), bop: pick(BIN_OPS), a: ri(), b: ri() };
      body.push(ins);
      remember(ins);
    } else if (kind < 0.58) {
      if (rnd() < 0.5) {
        const ins: Instr = { op: "div", dst: ri(), a: ri(), b: ri() };
        body.push(ins);
        remember(ins);
      } else {
        const ins: Instr = { op: "mod", dst: ri(), a: ri(), b: ri() };
        body.push(ins);
        remember(ins);
      }
    } else if (kind < 0.72) {
      let ins: Instr = { op: "load", dst: 0, addr: 0 };
      withAddr((addr) => {
        ins = { op: "load", dst: ri(), addr };
      });
      body.push(ins);
      remember(ins);
    } else if (kind < 0.88) {
      withAddr((addr) => {
        body.push({ op: "store", addr, src: ri() });
      });
    } else {
      body.push({ op: "assert", cond: ri() });
    }
  }
  body.push({ op: "ret", src: ri() });
  return { numRegs, numInputs, memSize, body };
}
