// Test suite:
//  1. Differential tests: TCO interpreter vs naive interpreter on small
//     programs — results AND call/return event order must match exactly.
//  2. Space tests: million-step tail recursion must run with O(1) frames
//     and a tail-call summary bounded by the configured cap.
//  3. Error-trace tests: full non-tail stack + bounded recent tail summary.

import { parseProgram } from '../src/lang';
import { runNaive } from '../src/naive';
import { runTco } from '../src/tco';
import {
  EventLog,
  InterpError,
  Value,
  formatTrace,
  showValue,
} from '../src/runtime';

let passed = 0;
let failed = 0;

function check(cond: boolean, label: string): void {
  if (cond) {
    passed++;
  } else {
    failed++;
    console.error(`FAIL: ${label}`);
  }
}

function eqJson(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

interface RunOutcome {
  value?: Value;
  error?: InterpError;
}

function runTcoSafe(src: string, cap: number, log?: EventLog): RunOutcome & { stats?: import('../src/tco').SpaceStats } {
  try {
    const r = runTco(parseProgram(src), { maxTailSummary: cap, log });
    return { value: r.value, stats: r.stats };
  } catch (e) {
    if (e instanceof InterpError) return { error: e };
    throw e;
  }
}

function runNaiveSafe(src: string, log?: EventLog): RunOutcome {
  try {
    return { value: runNaive(parseProgram(src), { log }) };
  } catch (e) {
    if (e instanceof InterpError) return { error: e };
    throw e;
  }
}

// ---------------------------------------------------------------------------
// 1. Differential tests
// ---------------------------------------------------------------------------

const DIFF_PROGRAMS: Array<[string, string]> = [
  ['arithmetic + if + let', `
    (defun area (w h) (* w h))
    (let (x (+ 1 2)) (+ (area x 4) (if (> x 2) 10 20)))
  `],
  ['tail factorial', `
    (defun fact-iter (n acc) (if (= n 0) acc (fact-iter (- n 1) (* acc n))))
    (defun fact (n) (fact-iter n 1))
    (fact 10)
  `],
  ['non-tail fib (call order)', `
    (defun fib (n) (if (< n 2) n (+ (fib (- n 1)) (fib (- n 2)))))
    (fib 10)
  `],
  ['mutual tail recursion', `
    (defun is-even (n) (if (= n 0) true (is-odd (- n 1))))
    (defun is-odd (n) (if (= n 0) false (is-even (- n 1))))
    (is-even 1001)
  `],
  ['non-tail sum', `
    (defun sum (n) (if (= n 0) 0 (+ n (sum (- n 1)))))
    (sum 100)
  `],
  ['mixed non-tail outer + tail inner', `
    (defun loop (n acc) (if (= n 0) acc (loop (- n 1) (+ acc 2))))
    (defun start (n) (+ 1 (loop n 0)))
    (start 500)
  `],
  ['argument evaluation order', `
    (defun g (x) (* x 10))
    (defun h (x) (+ x 1))
    (defun f (a b) (- a b))
    (f (g 2) (h 5))
  `],
  ['short-circuit and/or', `
    (defun boom (x) (error "should not run"))
    (begin (and false (boom 1)) (or true (boom 2)) (and true true))
  `],
  ['error in tail position', `
    (defun loop (n) (if (= n 0) (error "tail boom") (loop (- n 1))))
    (loop 10)
  `],
  ['error in non-tail position', `
    (defun inner (x) (error "inner boom"))
    (defun outer (y) (+ 1 (inner y)))
    (outer 3)
  `],
  ['division by zero in argument', `(+ 1 (div 10 0))`],
  ['unbound variable', `(+ x 1)`],
  ['arity mismatch', `
    (defun f (a b) a)
    (f 1)
  `],
  ['unknown function', `(nosuch 1)`],
  ['non-boolean if condition', `(if 1 2 3)`],
];

function differentialTests(): void {
  for (const [name, src] of DIFF_PROGRAMS) {
    const tcoLog = new EventLog();
    const naiveLog = new EventLog();
    const tco = runTcoSafe(src, 8, tcoLog);
    const naive = runNaiveSafe(src, naiveLog);

    const tcoOut = tco.error ? `error: ${tco.error.message}` : `value: ${showValue(tco.value!)}`;
    const naiveOut = naive.error ? `error: ${naive.error.message}` : `value: ${showValue(naive.value!)}`;
    check(tcoOut === naiveOut, `diff[${name}]: outcome ${tcoOut} vs ${naiveOut}`);
    check(
      eqJson(tcoLog.serialize(), naiveLog.serialize()),
      `diff[${name}]: call/return event order`,
    );
  }

  // For a purely non-tail error, the TCO full non-tail stack must equal the
  // naive interpreter's entire stack.
  const src = `
    (defun inner (x) (error "deep boom"))
    (defun mid (y) (+ 1 (inner y)))
    (defun outer (z) (* 2 (mid z)))
    (outer 7)
  `;
  const tco = runTcoSafe(src, 8);
  const naive = runNaiveSafe(src);
  check(
    !!tco.error && !!naive.error && eqJson(tco.error.trace.stack, naive.error.trace.stack),
    'diff[non-tail error]: full non-tail stack matches naive stack',
  );
}

// ---------------------------------------------------------------------------
// 2. Space tests: million-step tail recursion
// ---------------------------------------------------------------------------

function spaceTests(): void {
  const M = 1_000_000;

  const countdown = `
    (defun countdown (n) (if (= n 0) 0 (countdown (- n 1))))
    (countdown ${M})
  `;
  const r1 = runTco(parseProgram(countdown), { maxTailSummary: 16 });
  check(r1.value === 0, 'space[countdown]: result');
  check(r1.stats.tailCalls === M, `space[countdown]: tailCalls=${r1.stats.tailCalls}`);
  check(r1.stats.framesAllocated === 1, `space[countdown]: framesAllocated=${r1.stats.framesAllocated}`);
  check(r1.stats.maxStackDepth === 1, `space[countdown]: maxStackDepth=${r1.stats.maxStackDepth}`);
  check(r1.stats.tailSummaryMax === 16, `space[countdown]: tailSummaryMax=${r1.stats.tailSummaryMax}`);

  const mutual = `
    (defun is-even (n) (if (= n 0) true (is-odd (- n 1))))
    (defun is-odd (n) (if (= n 0) false (is-even (- n 1))))
    (is-even ${M})
  `;
  const r2 = runTco(parseProgram(mutual), { maxTailSummary: 4 });
  check(r2.value === true, 'space[mutual]: result');
  check(r2.stats.tailCalls === M, `space[mutual]: tailCalls=${r2.stats.tailCalls}`);
  check(r2.stats.framesAllocated === 1, `space[mutual]: framesAllocated=${r2.stats.framesAllocated}`);
  check(r2.stats.maxStackDepth === 1, `space[mutual]: maxStackDepth=${r2.stats.maxStackDepth}`);
  check(r2.stats.tailSummaryMax === 4, `space[mutual]: tailSummaryMax=${r2.stats.tailSummaryMax}`);

  const mixed = `
    (defun loop (n acc) (if (= n 0) acc (loop (- n 1) (+ acc 1))))
    (defun wrapper (n) (+ 1 (loop n 0)))
    (wrapper ${M})
  `;
  const r3 = runTco(parseProgram(mixed), { maxTailSummary: 8 });
  check(r3.value === M + 1, 'space[mixed]: result');
  check(r3.stats.framesAllocated === 2, `space[mixed]: framesAllocated=${r3.stats.framesAllocated}`);
  check(r3.stats.maxStackDepth === 2, `space[mixed]: maxStackDepth=${r3.stats.maxStackDepth}`);
  check(r3.stats.tailSummaryMax === 8, `space[mixed]: tailSummaryMax=${r3.stats.tailSummaryMax}`);

  // The cap is a hard bound: with cap 0 nothing is retained, and a huge cap
  // on a short chain retains only what happened.
  const r4 = runTco(parseProgram(countdown), { maxTailSummary: 0 });
  check(r4.stats.tailSummaryMax === 0, 'space[cap=0]: nothing retained');

  // Contrast: the naive interpreter cannot survive deep recursion.
  let naiveDepth = 'survived';
  try {
    runNaive(parseProgram(countdown.replace(String(M), '200000')));
  } catch (e) {
    naiveDepth = e instanceof RangeError ? 'stack overflow (RangeError)' : `other: ${e}`;
  }
  console.log(`  note: naive interpreter at 200k-deep recursion -> ${naiveDepth}`);
}

// ---------------------------------------------------------------------------
// 3. Error-trace tests
// ---------------------------------------------------------------------------

const TRACE_PROGRAM = `
  (defun loop (n) (if (= n 0) (error "boom") (loop (- n 1))))
  (defun g (x) (+ 0 (loop x)))
  (defun f (x) (+ 0 (g x)))
  (f 41)
`;
// f(41) -> g(41) -> loop(41) -> 41 tail self-calls -> error at loop(0).

function traceTests(): void {
  const cap = 8;
  const out = runTcoSafe(TRACE_PROGRAM, cap);
  const err = out.error;
  check(!!err && err.message === 'boom', 'trace: error raised');

  if (err) {
    const t = err.trace;
    check(
      eqJson(t.stack, [
        { fnName: 'f', args: [41] },
        { fnName: 'g', args: [41] },
        { fnName: 'loop', args: [0] },
      ]),
      `trace: full non-tail stack ${JSON.stringify(t.stack)}`,
    );
    check(t.totalTailCalls === 41, `trace: totalTailCalls=${t.totalTailCalls}`);
    check(t.tail.length === cap, `trace: retained tail summaries bounded at ${cap}`);
    check(
      t.tail.length === cap &&
        t.tail[0]!.ordinal === 34 &&
        t.tail[cap - 1]!.ordinal === 41 &&
        t.tail[cap - 1]!.fnName === 'loop' &&
        eqJson(t.tail[cap - 1]!.args, [0]),
      'trace: retained summaries are the MOST RECENT tail calls, with ordinals',
    );

    const text = formatTrace(t, err.message);
    check(text.includes('33 earlier tail call(s) omitted'), 'trace: format notes omitted count');
    check(text.includes('at f(41)') && text.includes('at g(41)') && text.includes('at loop(0)'),
      'trace: format lists full non-tail stack');
    check(text.includes('tail #41 loop(0)'), 'trace: format lists recent tail call');
  }

  // cap = 0: no summaries retained, but the count is still exact.
  const out0 = runTcoSafe(TRACE_PROGRAM, 0);
  check(
    !!out0.error && out0.error.trace.tail.length === 0 && out0.error.trace.totalTailCalls === 41,
    'trace[cap=0]: empty summary, exact total',
  );

  // cap larger than the chain: everything is retained, nothing invented.
  const outBig = runTcoSafe(TRACE_PROGRAM, 1000);
  check(
    !!outBig.error && outBig.error.trace.tail.length === 41 &&
    outBig.error.trace.tail[0]!.ordinal === 1,
    'trace[cap>chain]: all 41 summaries retained',
  );

  // Summary bound does not grow with recursion: 1M-step error run, cap 16.
  const deep = `
    (defun loop (n) (if (= n 0) (error "deep boom") (loop (- n 1))))
    (loop 1000000)
  `;
  const outDeep = runTcoSafe(deep, 16);
  check(
    !!outDeep.error &&
      outDeep.error.trace.tail.length === 16 &&
      outDeep.error.trace.totalTailCalls === 1_000_000 &&
      outDeep.error.trace.stack.length === 1 &&
      outDeep.error.trace.tail[15]!.ordinal === 1_000_000,
    'trace[1M steps]: summary stays bounded at 16, stack depth 1',
  );
}

// ---------------------------------------------------------------------------

differentialTests();
spaceTests();
traceTests();

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) throw new Error(`${failed} test(s) failed`);
