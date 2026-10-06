/**
 * Verification suite:
 *  1. Cross-check the optimized interpreter against the naive reference on
 *     small programs: identical results AND identical call-order traces.
 *  2. Error reports: complete non-tail stack + bounded recent-tail summary;
 *     consistency with the naive interpreter's full stack.
 *  3. Million-step tail recursion: constant frame counts (space accounting).
 */

import { ErrorReport, FrameInfo, InterpError, Program, Value, formatReport, formatValue } from "./lang";
import { NaiveInterpreter } from "./naive";
import { TailCallInterpreter } from "./optimized";
import * as ex from "./examples";

let passed = 0;
let failed = 0;

function check(name: string, cond: boolean, detail = ""): void {
  if (cond) {
    passed++;
    console.log(`  PASS ${name}`);
  } else {
    failed++;
    console.log(`  FAIL ${name}${detail ? ` -- ${detail}` : ""}`);
  }
}

function framesEqual(a: FrameInfo[], b: FrameInfo[]): boolean {
  if (a.length !== b.length) return false;
  return a.every(
    (f, i) =>
      f.funName === b[i].funName &&
      f.args.length === b[i].args.length &&
      f.args.every((x, j) => x === b[i].args[j])
  );
}

interface RunOut {
  value?: Value;
  error?: ErrorReport;
  trace: string[];
}

function runNaive(program: Program): RunOut {
  const interp = new NaiveInterpreter(program);
  try {
    return { value: interp.run(), trace: interp.trace };
  } catch (e) {
    if (e instanceof InterpError && e.snapshot) {
      return { error: e.snapshot, trace: interp.trace };
    }
    throw e;
  }
}

function runOptimized(program: Program, maxTailSummary: number, trace = true) {
  const interp = new TailCallInterpreter(program, maxTailSummary, trace);
  try {
    return { value: interp.run() as Value, trace: interp.trace, stats: interp.stats };
  } catch (e) {
    if (e instanceof InterpError && e.snapshot) {
      return { error: e.snapshot as ErrorReport, trace: interp.trace, stats: interp.stats };
    }
    throw e;
  }
}

// ---------------------------------------------------------------------------
console.log("\n== 1. Cross-check small programs: value + call-order trace ==");

const valueCases: Array<[string, Program, Value]> = [
  ["arith", ex.arithProgram, 14],
  ["factorial(6)", ex.factorialProgram(6), 720],
  ["countdown(1000)", ex.countdownProgram(1000), 0],
  ["even(1001)", ex.evenOddProgram(1001), false],
  ["even(1000)", ex.evenOddProgram(1000), true],
  ["sumTo(1000)", ex.sumToProgram(1000), 500500],
  ["call-order b(3)", ex.callOrderProgram, 12],
  ["fib(8)", ex.fibProgram(8), 21],
  ["begin sequencing", ex.beginProgram, 13],
];

for (const [name, program, expected] of valueCases) {
  const naive = runNaive(program);
  const opt = runOptimized(program, 8);
  check(
    `${name}: value`,
    naive.value === expected && opt.value === expected,
    `naive=${formatValue(naive.value!)} opt=${formatValue(opt.value!)} expected=${formatValue(expected)}`
  );
  check(
    `${name}: call-order trace identical`,
    naive.trace.length === opt.trace.length &&
      naive.trace.every((t, i) => t === opt.trace[i]),
    `naive ${naive.trace.length} entries vs opt ${opt.trace.length} entries`
  );
}

// ---------------------------------------------------------------------------
console.log("\n== 2. Error reports: complete non-tail stack + bounded tail summary ==");

{
  const program = ex.undefVarProgram;
  const naive = runNaive(program);
  const opt = runOptimized(program, 8);
  check("undef-var: both report same message",
    naive.error!.message === opt.error!.message);
  check("undef-var: non-tail stacks match naive full stack",
    framesEqual(opt.error!.nonTailStack, naive.error!.nonTailStack));
}

{
  // chain(5): non-tail frames <main>, chain(5); tail chain chain(4..0).
  const program = ex.tailChainErrorProgram(5);
  const naive = runNaive(program);
  const opt = runOptimized(program, 8); // cap larger than chain -> nothing elided
  const optFull = [...opt.error!.nonTailStack, ...opt.error!.recentTailCalls];
  check("tail-chain error: message", opt.error!.message === "boom at the bottom");
  check("tail-chain error: non-tail stack is [<main>, chain(5)]",
    framesEqual(opt.error!.nonTailStack, [
      { funName: "<main>", args: [] },
      { funName: "chain", args: [5] },
    ]));
  check("tail-chain error: 5 recent tail calls, 0 elided",
    opt.error!.recentTailCalls.length === 5 && opt.error!.tailElided === 0);
  check("tail-chain error: nonTail ++ recentTail == naive full stack",
    framesEqual(optFull, naive.error!.nonTailStack));
  check("tail-chain error: call-order traces identical",
    naive.trace.length === opt.trace.length &&
      naive.trace.every((t, i) => t === opt.trace[i]));
}

{
  // Same program, cap = 3: summary bounded, non-tail stack still complete.
  const opt = runOptimized(ex.tailChainErrorProgram(5), 3);
  check("tail-chain cap=3: only last 3 tail calls kept",
    opt.error!.recentTailCalls.length === 3 &&
    framesEqual(opt.error!.recentTailCalls, [
      { funName: "chain", args: [2] },
      { funName: "chain", args: [1] },
      { funName: "chain", args: [0] },
    ]));
  check("tail-chain cap=3: elided count = 2",
    opt.error!.tailElided === 2 && opt.error!.tailTotal === 5);
  check("tail-chain cap=3: non-tail stack still complete",
    framesEqual(opt.error!.nonTailStack, [
      { funName: "<main>", args: [] },
      { funName: "chain", args: [5] },
    ]));
}

{
  // cap = 0: summary disabled entirely, non-tail stack unaffected.
  const opt = runOptimized(ex.tailChainErrorProgram(5), 0);
  check("tail-chain cap=0: empty summary, all elided",
    opt.error!.recentTailCalls.length === 0 && opt.error!.tailElided === 5);
  check("tail-chain cap=0: non-tail stack complete",
    opt.error!.nonTailStack.length === 2);
}

{
  const program = ex.mixedErrorProgram(4);
  const naive = runNaive(program);
  const opt = runOptimized(program, 8);
  const optFull = [...opt.error!.nonTailStack, ...opt.error!.recentTailCalls];
  check("mixed error: non-tail stack is <main> -> outer -> inner(4)",
    framesEqual(opt.error!.nonTailStack, [
      { funName: "<main>", args: [] },
      { funName: "outer", args: [] },
      { funName: "inner", args: [4] },
    ]));
  check("mixed error: nonTail ++ recentTail == naive full stack",
    framesEqual(optFull, naive.error!.nonTailStack));
  console.log("\n  Sample formatted report (mixed error, cap 8):");
  console.log(formatReport(opt.error!).split("\n").map((l) => `    ${l}`).join("\n"));
  console.log("");
}

// ---------------------------------------------------------------------------
console.log("\n== 3. Million-step tail recursion: bounded space accounting ==");

const MILLION = 1_000_000;
const CAP = 5;

{
  const opt = runOptimized(ex.countdownProgram(MILLION), CAP, false);
  check("countdown(1e6): result 0", opt.value === 0);
  check("countdown(1e6): exactly 1 frame allocated (only <main>)",
    opt.stats.framesAllocated === 1, `framesAllocated=${opt.stats.framesAllocated}`);
  check("countdown(1e6): non-tail depth stays 1",
    opt.stats.maxNonTailDepth === 1, `maxNonTailDepth=${opt.stats.maxNonTailDepth}`);
  check("countdown(1e6): all 1,000,001 tail calls reused the frame",
    opt.stats.tailCalls === MILLION + 1, `tailCalls=${opt.stats.tailCalls}`);
}

{
  const opt = runOptimized(ex.evenOddProgram(MILLION), CAP, false);
  check("even(1e6) mutual tail recursion: result true", opt.value === true);
  check("even(1e6): exactly 1 frame allocated",
    opt.stats.framesAllocated === 1, `framesAllocated=${opt.stats.framesAllocated}`);
  check("even(1e6): 1,000,001 tail calls, depth 1",
    opt.stats.tailCalls === MILLION + 1 && opt.stats.maxNonTailDepth === 1);
}

{
  const opt = runOptimized(ex.sumToProgram(MILLION), CAP, false);
  check("sumTo(1e6): result 500000500000", opt.value === 500000500000);
  check("sumTo(1e6): exactly 1 frame allocated",
    opt.stats.framesAllocated === 1);
}

{
  // Error after a million tail steps: summary stays bounded, stack complete.
  const opt = runOptimized(ex.tailChainErrorProgram(MILLION), CAP, false);
  const err = opt.error!;
  check("error after 1e6 tail steps: message", err.message === "boom at the bottom");
  check("error after 1e6 tail steps: summary holds exactly cap=5 entries",
    err.recentTailCalls.length === CAP, `len=${err.recentTailCalls.length}`);
  check("error after 1e6 tail steps: kept entries are the LAST 5 (chain 4..0)",
    framesEqual(err.recentTailCalls, [
      { funName: "chain", args: [4] },
      { funName: "chain", args: [3] },
      { funName: "chain", args: [2] },
      { funName: "chain", args: [1] },
      { funName: "chain", args: [0] },
    ]));
  check("error after 1e6 tail steps: 999,996 elided of 1,000,000 total",
    err.tailElided === MILLION - CAP && err.tailTotal === MILLION,
    `elided=${err.tailElided} total=${err.tailTotal}`);
  check("error after 1e6 tail steps: non-tail stack complete",
    framesEqual(err.nonTailStack, [
      { funName: "<main>", args: [] },
      { funName: "chain", args: [MILLION] },
    ]));
  check("error after 1e6 tail steps: frames still O(1)",
    opt.stats.framesAllocated === 2 && opt.stats.maxNonTailDepth === 2,
    `framesAllocated=${opt.stats.framesAllocated} maxDepth=${opt.stats.maxNonTailDepth}`);
}

{
  // Non-tail recursion depth is honestly reported (no tail optimization hides it).
  const opt = runOptimized(ex.factorialProgram(50), CAP, false);
  check("fact(50) non-tail: depth grows to 51 (no hiding)",
    opt.stats.maxNonTailDepth === 51 && opt.stats.tailCalls === 0,
    `maxDepth=${opt.stats.maxNonTailDepth} tailCalls=${opt.stats.tailCalls}`);
}

// ---------------------------------------------------------------------------
console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
