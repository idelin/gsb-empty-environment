// Differential test: for bounded random programs and inputs, the original and
// CSE-optimized program must agree on return value, write trace, and the
// first exception.
import { optimize } from './cse';
import { fmtProgram } from './lang';
import { genInput, genProgram, mulberry32, Rng } from './gen';
import { Outcome, run, sameOutcome } from './interp';

function fmtOutcome(o: Outcome): string {
  const ws = o.writes.map((w) => `[${w.addr}]=${w.value}`).join(' ');
  return `ret=${o.ret} error=${o.error} writes={${ws}}`;
}

const iterations = process.argv[2] ? parseInt(process.argv[2], 10) : 20000;
const inputsPerProgram = 3;

let mismatches = 0;
let totalEliminated = 0;
let programsWithElim = 0;
let exceptionRuns = 0;
let totalRuns = 0;
let errorKinds = new Map<string, number>();

for (let seed = 1; seed <= iterations; seed++) {
  const rng = new Rng(mulberry32(seed * 2654435761));
  const prog = genProgram(rng);
  const opt = optimize(prog);
  totalEliminated += opt.eliminated;
  if (opt.eliminated > 0) programsWithElim++;

  for (let k = 0; k < inputsPerProgram; k++) {
    const input = genInput(rng);
    const a = run(prog, input);
    const b = run(opt.prog, input);
    totalRuns++;
    if (a.error) {
      exceptionRuns++;
      errorKinds.set(a.error, (errorKinds.get(a.error) ?? 0) + 1);
    }
    if (!sameOutcome(a, b)) {
      mismatches++;
      console.error(`MISMATCH seed=${seed} input#${k}`);
      console.error(`input: ${JSON.stringify(input)}`);
      console.error(`--- original (${prog.instrs.length} instrs) ---\n${fmtProgram(prog)}`);
      console.error(`--- optimized (${opt.prog.instrs.length} instrs) ---\n${fmtProgram(opt.prog)}`);
      console.error(`original : ${fmtOutcome(a)}`);
      console.error(`optimized: ${fmtOutcome(b)}`);
      if (mismatches >= 3) {
        console.error('too many mismatches, aborting');
        process.exit(1);
      }
    }
  }
}

console.log(`programs:            ${iterations}`);
console.log(`runs:                ${totalRuns}`);
console.log(`runs with exception: ${exceptionRuns} (${((exceptionRuns / totalRuns) * 100).toFixed(1)}%)`);
for (const [kind, n] of [...errorKinds.entries()].sort()) {
  console.log(`  ${kind}: ${n}`);
}
console.log(`instrs eliminated:   ${totalEliminated} (in ${programsWithElim} programs)`);
console.log(`mismatches:          ${mismatches}`);
process.exit(mismatches === 0 ? 0 : 1);
