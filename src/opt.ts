// Common subexpression elimination by value numbering, in a single forward
// pass over the straight-line block.
//
// Soundness rules (this is where naive text matching goes wrong):
//
// 1. Aliasing. A `store` kills remembered `load` values whose address MAY
//    alias the stored address. Two addresses provably do not alias only when
//    both are known distinct constants; otherwise we conservatively kill.
//    A store also enables store-to-load forwarding: a later `load` from the
//    SAME value number (must-alias) reads the stored value.
//
// 2. Exception order. Potentially trapping instructions (div/mod/load/
//    store/assert) are never removed, never introduced, and never moved
//    across each other. CSE only replaces the LATER of two identical
//    computations with a copy of the earlier result. In straight-line code,
//    reaching the second computation implies the first one did not trap, so
//    the second cannot trap either (same operands, deterministic semantics).
//    Removing the EARLIER occurrence instead would be wrong: it would delay
//    the trap past intervening stores, changing the write trace.
//
// 3. Constant folding. Pure ops fold freely. div/mod fold only when the
//    divisor is a known NONZERO constant; a div by a zero constant is a
//    mandatory trap at exactly that program point and is kept as-is.
//
// 4. assert. `assert c` on a known-nonzero value, or on a value already
//    asserted (same value number, so it cannot have changed), is dropped.
//    `assert` on a known-zero value is kept (mandatory trap).
//
// The pass never reorders instructions and never grows the block.

import type { Instr, Prog, Reg } from "./lang.ts";
import { evalBin, evalDiv, evalMod } from "./lang.ts";

type Vn = number; // value number: names a value, independent of which register holds it

export function optimize(p: Prog): Prog {
  const out: Instr[] = [];

  const vn = new Map<Reg, Vn>();        // register -> value it currently holds
  const constOf = new Map<Vn, number>(); // value -> constant, if known
  const holder = new Map<Vn, Reg>();     // value -> a register currently holding it
  const leader = new Map<string, Vn>();  // expression key -> value (pure + checked exprs, and "c:<k>")
  const loadLeader = new Map<Vn, Vn>();  // address value -> loaded value (current memory state)
  const storeVal = new Map<Vn, Vn>();    // address value -> value stored there (current memory state)
  const asserted = new Set<Vn>();        // values known nonzero because an assert passed
  let nextVn = 0;

  const freshVn = (): Vn => nextVn++;

  const constVn = (k: number): Vn => {
    const key = "c:" + k;
    let v = leader.get(key);
    if (v === undefined) {
      v = freshVn();
      leader.set(key, v);
      constOf.set(v, k);
    }
    return v;
  };

  // Record that register r now holds value v.
  const setVn = (r: Reg, v: Vn): void => {
    const old = vn.get(r);
    if (old !== undefined && holder.get(old) === r) {
      holder.delete(old);
      // Another register may still hold the old value; keep it usable.
      for (const [rr, vv] of vn) {
        if (rr !== r && vv === old) {
          holder.set(old, rr);
          break;
        }
      }
    }
    vn.set(r, v);
    holder.set(v, r);
  };

  // Can the value be materialized into a register right here?
  const available = (v: Vn): boolean => constOf.has(v) || holder.has(v);

  // Conservative alias test: only two known distinct constants cannot alias.
  const mayAlias = (x: Vn, y: Vn): boolean => {
    const cx = constOf.get(x);
    const cy = constOf.get(y);
    if (cx !== undefined && cy !== undefined) return cx === cy;
    return true;
  };

  // Replace the current instruction by a cheap copy of an already-computed value.
  const materialize = (dst: Reg, v: Vn): void => {
    if (vn.get(dst) === v) return; // dst already holds exactly this value
    const c = constOf.get(v);
    if (c !== undefined) out.push({ op: "const", dst, value: c });
    else out.push({ op: "mov", dst, src: holder.get(v)! });
    setVn(dst, v);
  };

  // Initially each register holds a distinct unknown input value.
  for (let r = 0; r < p.numRegs; r++) {
    const v = freshVn();
    vn.set(r, v);
    holder.set(v, r);
  }

  for (const ins of p.body) {
    switch (ins.op) {
      case "const": {
        out.push(ins);
        setVn(ins.dst, constVn(ins.value | 0));
        break;
      }

      case "mov": {
        out.push(ins);
        setVn(ins.dst, vn.get(ins.src)!);
        break;
      }

      case "bin": {
        const va = vn.get(ins.a)!;
        const vb = vn.get(ins.b)!;
        const ca = constOf.get(va);
        const cb = constOf.get(vb);
        if (ca !== undefined && cb !== undefined) {
          // Pure op: folding can never remove a trap.
          const k = evalBin(ins.bop, ca, cb);
          out.push({ op: "const", dst: ins.dst, value: k });
          setVn(ins.dst, constVn(k));
          break;
        }
        const key = `bin:${ins.bop}:${va}:${vb}`;
        const existing = leader.get(key);
        if (existing !== undefined && available(existing)) {
          materialize(ins.dst, existing);
        } else {
          const v = freshVn();
          leader.set(key, v);
          out.push(ins);
          setVn(ins.dst, v);
        }
        break;
      }

      case "div":
      case "mod": {
        const va = vn.get(ins.a)!;
        const vb = vn.get(ins.b)!;
        const ca = constOf.get(va);
        const cb = constOf.get(vb);
        if (ca !== undefined && cb !== undefined && cb !== 0) {
          // Provably non-trapping: safe to fold.
          const k = ins.op === "div" ? evalDiv(ca, cb) : evalMod(ca, cb);
          out.push({ op: "const", dst: ins.dst, value: k });
          setVn(ins.dst, constVn(k));
          break;
        }
        // cb === 0: this instruction is a mandatory trap; do NOT fold it away.
        const key = `${ins.op}:${va}:${vb}`;
        const existing = leader.get(key);
        if (existing !== undefined && available(existing)) {
          // Only the LATER occurrence is ever replaced. Reaching it means the
          // identical earlier op did not trap, so this one cannot trap either.
          materialize(ins.dst, existing);
        } else {
          const v = freshVn();
          leader.set(key, v);
          out.push(ins);
          setVn(ins.dst, v);
        }
        break;
      }

      case "load": {
        const va = vn.get(ins.addr)!;
        // Store-to-load forwarding: same value number = must-alias.
        const sv = storeVal.get(va);
        if (sv !== undefined && available(sv)) {
          materialize(ins.dst, sv);
          break;
        }
        const existing = loadLeader.get(va);
        if (existing !== undefined && available(existing)) {
          materialize(ins.dst, existing);
        } else {
          const v = freshVn();
          loadLeader.set(va, v);
          out.push(ins);
          setVn(ins.dst, v);
        }
        break;
      }

      case "store": {
        // Stores are observable (write trace) and may trap: never eliminated,
        // never moved. They only invalidate cached memory facts.
        out.push(ins);
        const va = vn.get(ins.addr)!;
        for (const k of [...loadLeader.keys()]) {
          if (mayAlias(va, k)) loadLeader.delete(k);
        }
        for (const k of [...storeVal.keys()]) {
          if (mayAlias(va, k)) storeVal.delete(k);
        }
        storeVal.set(va, vn.get(ins.src)!);
        break;
      }

      case "assert": {
        const vc = vn.get(ins.cond)!;
        const cc = constOf.get(vc);
        if (cc !== undefined) {
          if (cc !== 0) break; // never traps: drop
          out.push(ins); // always traps: keep (a trap is observable)
          break;
        }
        if (asserted.has(vc)) break; // same value already checked nonzero
        asserted.add(vc);
        out.push(ins);
        break;
      }

      case "ret": {
        out.push(ins);
        break;
      }
    }
  }

  return { ...p, body: out };
}
