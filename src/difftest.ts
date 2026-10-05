// Differential validator: for each seeded random program, run the original
// and the optimized version on a finite set of inputs (exhaustive over a
// small domain + random wider samples) and require identical observable
// behavior: return value, write trace, and first trap.

import { genProgram, mulberry32 } from "./gen.ts";
import { run } from "./interp.ts";
import { optimize } from "./opt.ts";
import { fmtProg } from "./lang.ts";
import type { Input, Prog } from "./lang.ts";

function product(vals: number[], n: number): number[][] {
  let acc: number[][] = [[]];
  for (let i = 0; i < n; i++) {
    const next: number[][] = [];
    for (const combo of acc) {
      for (const v of vals) next.push([...combo, v]);
    }
    acc = next;
  }
  return acc;
}

function* allInputs(p: Prog, seed: number): Generator<Input> {
  // Exhaustive over a compact domain: 3^numInputs * 2^memSize combos.
  for (const regs of product([-1, 0, 1], p.numInputs)) {
    for (const mem of product([0, 1], p.memSize)) {
      yield { regs, mem };
    }
  }
  // Wider random samples (oob addresses, negative divisors, ...).
  const rnd = mulberry32(seed ^ 0x9e3779b9);
  for (let i = 0; i < 16; i++) {
    yield {
      regs: Array.from({ length: p.numInputs }, () => Math.floor(rnd() * 9) - 3),
      mem: Array.from({ length: p.memSize }, () => Math.floor(rnd() * 9) - 2),
    };
  }
}

function bomb(msg: string, seed: number, prog: Prog, opt: Prog, extra: string): never {
  console.error(`FAIL (${msg}) seed=${seed}`);
  console.error("original:\n" + fmtProg(prog));
  console.error("optimized:\n" + fmtProg(opt));
  console.error(extra);
  process.exit(1);
}

const seeds = Number(process.argv[2] ?? "2000");
const t0 = Date.now();
let totalInputs = 0;
let eliminated = 0;

for (let seed = 1; seed <= seeds; seed++) {
  const prog = genProgram(seed);
  const opt = optimize(prog);
  eliminated += prog.body.length - opt.body.length;

  if (opt.body.length > prog.body.length) {
    bomb("optimizer grew the program", seed, prog, opt, "");
  }
  const opt2 = optimize(opt);
  if (JSON.stringify(opt2) !== JSON.stringify(opt)) {
    bomb("optimizer is not idempotent", seed, prog, opt, "second pass:\n" + fmtProg(opt2));
  }

  for (const input of allInputs(prog, seed)) {
    totalInputs++;
    const a = run(prog, input);
    const b = run(opt, input);
    if (JSON.stringify(a) !== JSON.stringify(b)) {
      bomb(
        "observable behavior differs",
        seed,
        prog,
        opt,
        `input:     ${JSON.stringify(input)}\n` +
          `original:  ${JSON.stringify(a)}\n` +
          `optimized: ${JSON.stringify(b)}`,
      );
    }
  }
}

console.log(
  `OK: ${seeds} programs, ${totalInputs} input pairs, 0 mismatches, ` +
    `${eliminated} instructions eliminated, ${Date.now() - t0}ms`,
);
