// Common subexpression elimination over a straight-line SSA program.
//
// Legality rules (this is where naive text matching goes wrong):
//
// 1. Aliasing stores. A `load` may only be replaced by an earlier load/store
//    result for the same address temp if NO store executed in between. Since
//    addresses are dynamic values, any intervening store may alias, so a store
//    conservatively kills every cached load fact. (Pure ops and div/mod do not
//    touch memory and are unaffected by stores.)
//
// 2. Exception order. We never reorder instructions and never remove the
//    FIRST occurrence of a potentially-throwing computation. A later
//    duplicate `div a, b` / `load a` is only removed when an identical
//    instruction strictly precedes it; if the earlier one did not throw, the
//    later one (same operands, SSA => same values, same address) cannot throw
//    either, so removing it preserves the first-exception behavior exactly.
//    Store-to-load forwarding is likewise safe: the store already proved the
//    address in bounds.
//
// Because temps are SSA, no expression is ever invalidated by redefinition;
// only stores invalidate (load facts).

import { COMMUTATIVE, Instr, Program, Temp } from './lang';

export interface OptResult {
  prog: Program;
  eliminated: number;
}

export function optimize(prog: Program): OptResult {
  const out: Instr[] = [];
  // expr key -> temp holding its value (only for non-store instructions)
  const avail = new Map<string, Temp>();
  // store-to-load forwarding: address temp -> temp holding memory[addr]
  const forwarded = new Map<Temp, Temp>();
  // eliminated temp -> the temp that replaces it
  const rename = new Map<Temp, Temp>();
  let eliminated = 0;

  const resolve = (t: Temp): Temp => {
    let cur = t;
    for (;;) {
      const next = rename.get(cur);
      if (next === undefined) return cur;
      cur = next;
    }
  };

  const killLoadFacts = (): void => {
    for (const key of [...avail.keys()]) {
      if (key.startsWith('load:')) avail.delete(key);
    }
    forwarded.clear();
  };

  const dedup = (key: string, dst: Temp): boolean => {
    // Returns true if dst was eliminated as a duplicate of an existing temp.
    const prev = avail.get(key);
    if (prev !== undefined) {
      rename.set(dst, prev);
      eliminated++;
      return true;
    }
    avail.set(key, dst);
    return false;
  };

  for (const ins of prog.instrs) {
    switch (ins.kind) {
      case 'const': {
        if (!dedup(`const:${ins.value}`, ins.dst)) out.push(ins);
        break;
      }
      case 'input': {
        if (!dedup(`input:${ins.index}`, ins.dst)) out.push(ins);
        break;
      }
      case 'binop': {
        let a = resolve(ins.a);
        let b = resolve(ins.b);
        if (COMMUTATIVE.has(ins.op) && b < a) {
          const tmp = a; a = b; b = tmp;
        }
        if (!dedup(`bin:${ins.op}:${a}:${b}`, ins.dst)) {
          out.push({ ...ins, a, b });
        }
        break;
      }
      case 'divmod': {
        // May throw, but a duplicate after a non-throwing twin cannot throw
        // (same operands), so keeping the earliest occurrence is exact.
        const a = resolve(ins.a);
        const b = resolve(ins.b);
        if (!dedup(`div:${ins.op}:${a}:${b}`, ins.dst)) {
          out.push({ ...ins, a, b });
        }
        break;
      }
      case 'load': {
        const addr = resolve(ins.addr);
        const fwd = forwarded.get(addr);
        if (fwd !== undefined) {
          // memory[addr] is known from a preceding load/store with no
          // intervening store; the earlier access already proved bounds.
          rename.set(ins.dst, fwd);
          eliminated++;
          break;
        }
        if (!dedup(`load:${addr}`, ins.dst)) {
          out.push({ ...ins, addr });
          forwarded.set(addr, ins.dst);
        }
        break;
      }
      case 'store': {
        const addr = resolve(ins.addr);
        const value = resolve(ins.value);
        out.push({ kind: 'store', addr, value });
        // Any store may alias any address: all cached load facts die.
        killLoadFacts();
        forwarded.set(addr, value);
        break;
      }
      case 'ret': {
        out.push({ kind: 'ret', value: resolve(ins.value) });
        break;
      }
    }
  }
  return { prog: { instrs: out }, eliminated };
}
