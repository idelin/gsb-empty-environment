// Hand-written unit tests for the tricky cases: aliasing, store-to-load
// forwarding, exception ordering, and trap-preserving folding. Each case is
// also differentially checked (original vs optimized) on a set of inputs.

import { run } from "./interp.ts";
import { optimize } from "./opt.ts";
import { fmtProg } from "./lang.ts";
import type { Input, Instr, Prog } from "./lang.ts";

let failures = 0;

function check(name: string, cond: boolean, detail: string): void {
  if (cond) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.log(`FAIL ${name}\n${detail}`);
  }
}

function countOps(p: Prog, op: Instr["op"]): number {
  return p.body.filter((i) => i.op === op).length;
}

function diffOnInputs(name: string, prog: Prog, opt: Prog, inputs: Input[]): void {
  for (const input of inputs) {
    const a = run(prog, input);
    const b = run(opt, input);
    if (JSON.stringify(a) !== JSON.stringify(b)) {
      check(
        name,
        false,
        `input ${JSON.stringify(input)}\noriginal:\n${fmtProg(prog)}\noptimized:\n${fmtProg(opt)}\n` +
          `original:  ${JSON.stringify(a)}\noptimized: ${JSON.stringify(b)}`,
      );
      return;
    }
  }
  check(name, true, "");
}

const P = (numInputs: number, body: Instr[]): Prog => ({
  numRegs: 6,
  numInputs,
  memSize: 4,
  body,
});

const INPUTS2: Input[] = [];
for (const x of [-1, 0, 1, 2, 5]) {
  for (const y of [-1, 0, 1, 3]) {
    for (const mem of [[0, 0, 0, 0], [1, 2, 3, 4], [-1, 0, 7, 2]]) {
      INPUTS2.push({ regs: [x, y], mem });
    }
  }
}

// A. store to the SAME address: later load must see the stored value
//    (store-to-load forwarding), not the stale loaded value.
{
  const prog = P(1, [
    { op: "const", dst: 1, value: 7 },
    { op: "load", dst: 2, addr: 0 },   // x = mem[p]
    { op: "store", addr: 0, src: 1 },  // mem[p] = 7
    { op: "load", dst: 3, addr: 0 },   // y = mem[p]  -> must be 7
    { op: "bin", dst: 4, bop: "add", a: 2, b: 3 },
    { op: "ret", src: 4 },
  ]);
  const opt = optimize(prog);
  check("A: must-alias store kills stale load", countOps(opt, "load") === 1, fmtProg(opt));
  diffOnInputs("A: diff", prog, opt, INPUTS2);
}

// B. store through an UNKNOWN address may alias: no load CSE across it.
{
  const prog = P(2, [
    { op: "load", dst: 2, addr: 0 },   // x = mem[p]
    { op: "const", dst: 3, value: 9 },
    { op: "store", addr: 1, src: 3 },  // mem[q] = 9, q unknown
    { op: "load", dst: 4, addr: 0 },   // y = mem[p]  -> NOT replaceable
    { op: "ret", src: 4 },
  ]);
  const opt = optimize(prog);
  check("B: may-alias store blocks load CSE", countOps(opt, "load") === 2, fmtProg(opt));
  diffOnInputs("B: diff", prog, opt, INPUTS2);
}

// C. store to a provably DIFFERENT constant address: load CSE still fires.
{
  const prog = P(0, [
    { op: "const", dst: 0, value: 0 },
    { op: "const", dst: 1, value: 1 },
    { op: "load", dst: 2, addr: 0 },   // x = mem[0]
    { op: "const", dst: 3, value: 5 },
    { op: "store", addr: 1, src: 3 },  // mem[1] = 5, cannot alias mem[0]
    { op: "load", dst: 4, addr: 0 },   // y = mem[0] -> mov from x
    { op: "bin", dst: 5, bop: "add", a: 2, b: 4 },
    { op: "ret", src: 5 },
  ]);
  const opt = optimize(prog);
  check("C: non-aliasing const store keeps load CSE", countOps(opt, "load") === 1, fmtProg(opt));
  diffOnInputs("C: diff", prog, opt, INPUTS2);
}

// D. exception order: the FIRST div must stay before the store; the second
//    div is replaced by a copy. With b == 0 the trap must happen before any
//    write is recorded.
{
  const prog = P(2, [
    { op: "div", dst: 2, a: 0, b: 1 },  // may trap here
    { op: "const", dst: 3, value: 0 },
    { op: "const", dst: 4, value: 42 },
    { op: "store", addr: 3, src: 4 },   // observable write
    { op: "div", dst: 5, a: 0, b: 1 },  // redundant -> mov r5, r2
    { op: "ret", src: 5 },
  ]);
  const opt = optimize(prog);
  check("D: redundant div eliminated, first div kept", countOps(opt, "div") === 1, fmtProg(opt));
  const trapped = run(opt, { regs: [7, 0], mem: [0, 0, 0, 0] });
  check(
    "D: trap still precedes the store",
    trapped.kind === "throw" && trapped.error === "div-by-zero" && trapped.writes.length === 0,
    JSON.stringify(trapped),
  );
  diffOnInputs("D: diff", prog, opt, INPUTS2);
}

// E. assert on an already-checked value, or on a nonzero constant, is dropped.
{
  const prog = P(1, [
    { op: "assert", cond: 0 },
    { op: "assert", cond: 0 },          // duplicate -> drop
    { op: "const", dst: 1, value: 3 },
    { op: "assert", cond: 1 },          // never traps -> drop
    { op: "ret", src: 0 },
  ]);
  const opt = optimize(prog);
  check(
    "E: redundant asserts removed",
    countOps(opt, "assert") === 1 && opt.body.length === 3,
    fmtProg(opt),
  );
  diffOnInputs("E: diff", prog, opt, INPUTS2);
}

// F. div by a zero constant is a mandatory trap: it must NOT be folded away.
{
  const prog = P(0, [
    { op: "const", dst: 0, value: 10 },
    { op: "const", dst: 1, value: 0 },
    { op: "div", dst: 2, a: 0, b: 1 },
    { op: "ret", src: 2 },
  ]);
  const opt = optimize(prog);
  const out = run(opt, { regs: [], mem: [0, 0, 0, 0] });
  check(
    "F: div-by-zero-constant kept as a trap",
    countOps(opt, "div") === 1 && out.kind === "throw" && out.error === "div-by-zero",
    fmtProg(opt) + "\n" + JSON.stringify(out),
  );
  diffOnInputs("F: diff", prog, opt, INPUTS2);
}

// G. plain CSE + constant folding of pure ops.
{
  const prog = P(2, [
    { op: "bin", dst: 2, bop: "add", a: 0, b: 1 },
    { op: "bin", dst: 3, bop: "add", a: 0, b: 1 }, // -> mov r3, r2
    { op: "bin", dst: 4, bop: "mul", a: 3, b: 2 },
    { op: "const", dst: 5, value: 2 },
    { op: "bin", dst: 5, bop: "add", a: 5, b: 5 }, // -> const 4
    { op: "ret", src: 4 },
  ]);
  const opt = optimize(prog);
  check(
    "G: CSE + pure folding",
    countOps(opt, "bin") === 2 && countOps(opt, "mov") === 1,
    fmtProg(opt),
  );
  diffOnInputs("G: diff", prog, opt, INPUTS2);
}

// H. two stores to the same address: forwarding sees the latest value.
{
  const prog = P(0, [
    { op: "const", dst: 0, value: 0 },
    { op: "const", dst: 1, value: 1 },
    { op: "store", addr: 0, src: 1 },
    { op: "const", dst: 2, value: 2 },
    { op: "store", addr: 0, src: 2 },
    { op: "load", dst: 3, addr: 0 },    // -> const 2
    { op: "ret", src: 3 },
  ]);
  const opt = optimize(prog);
  const out = run(opt, { regs: [], mem: [0, 0, 0, 0] });
  check(
    "H: forwarding uses latest store",
    countOps(opt, "load") === 0 && out.kind === "return" && out.value === 2,
    fmtProg(opt) + "\n" + JSON.stringify(out),
  );
  diffOnInputs("H: diff", prog, opt, INPUTS2);
}

// I. load CSE across a (possibly trapping) div is fine: if the div traps,
//    the second load is never reached in either version.
{
  const prog = P(2, [
    { op: "load", dst: 2, addr: 0 },   // x = mem[p]
    { op: "div", dst: 3, a: 0, b: 1 }, // may trap
    { op: "load", dst: 4, addr: 0 },   // -> mov r4, r2 (no store in between)
    { op: "bin", dst: 5, bop: "add", a: 2, b: 4 },
    { op: "ret", src: 5 },
  ]);
  const opt = optimize(prog);
  check("I: load CSE across trapping div", countOps(opt, "load") === 1, fmtProg(opt));
  diffOnInputs("I: diff", prog, opt, INPUTS2);
}

if (failures > 0) {
  console.log(`${failures} test(s) failed`);
  process.exit(1);
}
console.log("all unit tests passed");
