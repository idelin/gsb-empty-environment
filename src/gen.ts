// Seeded random program/input generator for differential testing.
import { DivOp, Instr, Program, PureOp, Temp } from './lang';
import { MachineInput } from './interp';

export function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export class Rng {
  constructor(private readonly next: () => number) {}
  next01(): number {
    return this.next();
  }
  int(lo: number, hi: number): number {
    // inclusive both ends
    return lo + Math.floor(this.next() * (hi - lo + 1));
  }
  pick<T>(xs: readonly T[]): T {
    return xs[this.int(0, xs.length - 1)]!;
  }
  chance(p: number): boolean {
    return this.next() < p;
  }
}

const PURE_OPS: readonly PureOp[] = ['add', 'sub', 'mul', 'and', 'or', 'xor', 'shl', 'shr'];
const DIV_OPS: readonly DivOp[] = ['div', 'mod'];

export const MEM_SIZE = 8;
export const NUM_INPUTS = 3;

export function genInput(rng: Rng): MachineInput {
  const inputs: number[] = [];
  for (let i = 0; i < NUM_INPUTS; i++) inputs.push(rng.int(-2, 3));
  const memory: number[] = [];
  for (let i = 0; i < MEM_SIZE; i++) memory.push(rng.int(-3, 4));
  return { inputs, memory };
}

export function genProgram(rng: Rng): Program {
  const instrs: Instr[] = [];
  const live: Temp[] = [];
  let nextTemp = 0;
  const fresh = (): Temp => nextTemp++;
  const someTemp = (): Temp => rng.pick(live);

  // Small constants make addresses collide (aliasing) and divisors hit zero.
  const len = rng.int(6, 24);
  for (let i = 0; i < len; i++) {
    const r = rng.chance(1) ? rng.int(0, 999) / 999 : 0;
    if (r < 0.18 || live.length === 0) {
      instrs.push({ kind: 'const', dst: fresh(), value: rng.int(-2, 9) });
    } else if (r < 0.26) {
      instrs.push({ kind: 'input', dst: fresh(), index: rng.int(0, NUM_INPUTS - 1) });
    } else if (r < 0.5) {
      instrs.push({ kind: 'binop', dst: fresh(), op: rng.pick(PURE_OPS), a: someTemp(), b: someTemp() });
    } else if (r < 0.62) {
      instrs.push({ kind: 'divmod', dst: fresh(), op: rng.pick(DIV_OPS), a: someTemp(), b: someTemp() });
    } else if (r < 0.78) {
      instrs.push({ kind: 'load', dst: fresh(), addr: someTemp() });
    } else if (r < 0.92) {
      instrs.push({ kind: 'store', addr: someTemp(), value: someTemp() });
      continue; // store defines no temp
    } else {
      // Deliberately clone an earlier defining instruction with a fresh dst
      // to create CSE opportunities (textually identical, but legality still
      // depends on intervening stores and exception order).
      const candidates = instrs.filter(
        (x) => x.kind !== 'store' && x.kind !== 'ret',
      ) as Extract<Instr, { dst: Temp }>[];
      if (candidates.length === 0) continue;
      const src = rng.pick(candidates);
      instrs.push({ ...src, dst: fresh() });
    }
    live.push(nextTemp - 1);
  }
  instrs.push({ kind: 'ret', value: rng.pick(live) });
  return { instrs };
}
