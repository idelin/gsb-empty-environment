// Differential tester: original vs optimized AST must agree on
//   - success/failure and the exact error message,
//   - the result value,
//   - the sequence of impure (side-effecting) calls, in order,
//   - the final side-effect counters.
// Pure calls may legitimately differ (deduplicated / folded), so only the
// impure subsequence of the trace is compared.

import type { Expr } from './ast.ts';
import { countNodes, countShared, show } from './ast.ts';
import { impureTrace, run } from './interpreter.ts';
import type { RunResult } from './interpreter.ts';
import { optimize } from './optimizer.ts';
import { randomExpr } from './randomAst.ts';

function lit(v: number): Expr {
  return { kind: 'lit', value: v };
}
function call(fn: string, ...args: Expr[]): Expr {
  return { kind: 'call', fn, args };
}
function ife(cond: Expr, then: Expr, els: Expr): Expr {
  return { kind: 'if', cond, then, else: els };
}

const DIRECTED: Array<[string, Expr]> = [
  // error must surface only after tick(1) has executed
  ['effect-before-error', call('add', call('tick', lit(1)), call('div', lit(1), lit(0)))],
  // dead lazy branch must never execute
  ['lazy-branch-not-taken', ife(lit(0), call('div', lit(1), lit(0)), lit(42))],
  // duplicated throwing pure subtree on both lazy branches: share, but never hoist
  ['throwing-dup-then', ife(lit(1), call('div', lit(1), lit(0)), call('div', lit(1), lit(0)))],
  ['throwing-dup-else', ife(lit(0), call('div', lit(1), lit(0)), call('div', lit(1), lit(0)))],
  // duplicated pure partial subtree in strict position: evaluate once, reuse
  ['dup-partial-strict', call('add', call('div', lit(6), lit(2)), call('div', lit(6), lit(2)))],
  // side-effect order is observable and must be preserved
  ['effect-order', call('add', call('tick', lit(1)), call('tick', lit(2)))],
  // impure calls must never be deduplicated
  ['impure-not-shared', ife(call('bump'), call('bump'), call('bump'))],
  // pure duplicate interleaved with effects: first occurrence position is kept
  ['dup-around-effect', call('add', call('sqrt', lit(9)), call('tick', call('sqrt', lit(9))))],
];

function compare(label: string, original: Expr, optimized: Expr): string | null {
  const r1 = run(original);
  const r2 = run(optimized);
  const problems: string[] = [];
  if (r1.ok !== r2.ok) {
    problems.push(`status: original ${r1.ok ? 'ok' : 'error'} vs optimized ${r2.ok ? 'ok' : 'error'}`);
  }
  if (r1.ok && r2.ok && !Object.is(r1.value, r2.value)) {
    problems.push(`value: ${r1.value} vs ${r2.value}`);
  }
  if (!r1.ok && !r2.ok && r1.error !== r2.error) {
    problems.push(`error: ${r1.error!} vs ${r2.error!}`);
  }
  const t1 = impureTrace(r1);
  const t2 = impureTrace(r2);
  if (t1 !== t2) {
    problems.push(`impure trace: ${t1} vs ${t2}`);
  }
  if (JSON.stringify(r1.counters) !== JSON.stringify(r2.counters)) {
    problems.push(`counters: ${JSON.stringify(r1.counters)} vs ${JSON.stringify(r2.counters)}`);
  }
  if (problems.length === 0) return null;
  return [
    `MISMATCH [${label}]`,
    `  original : ${show(original)}`,
    `  optimized: ${show(optimized)}`,
    ...problems.map((p) => `  ${p}`),
  ].join('\n');
}

function main(): void {
  const n = Number(process.argv[2] ?? '5000');
  let failures = 0;

  for (const [name, ast] of DIRECTED) {
    const problem = compare(`directed:${name}`, ast, optimize(ast));
    if (problem) {
      failures++;
      console.log(problem);
    }
  }
  console.log(`directed tests: ${DIRECTED.length - failures}/${DIRECTED.length} passed`);

  let errors = 0;
  let nodesBefore = 0;
  let nodesAfter = 0;
  let sharedIntroduced = 0;
  let astsWithSharing = 0;
  let impureCallsTotal = 0;

  for (let seed = 1; seed <= n; seed++) {
    const ast = randomExpr(seed);
    const opt = optimize(ast);
    const problem = compare(`seed=${seed}`, ast, opt);
    if (problem) {
      failures++;
      if (failures <= 5) console.log(problem);
      continue;
    }
    const r = run(ast);
    if (!r.ok) errors++;
    impureCallsTotal += r.trace.filter((t) => t.impure).length;
    nodesBefore += countNodes(ast);
    nodesAfter += countNodes(opt);
    const sh = countShared(opt);
    sharedIntroduced += sh;
    if (sh > 0) astsWithSharing++;
  }

  console.log(`random ASTs: ${n} compared, ${failures} mismatches`);
  console.log(`  errored runs        : ${errors} (${((100 * errors) / n).toFixed(1)}%)`);
  console.log(`  impure calls/run avg: ${(impureCallsTotal / n).toFixed(2)}`);
  console.log(`  nodes before/after  : ${(nodesBefore / n).toFixed(1)} -> ${(nodesAfter / n).toFixed(1)}`);
  console.log(`  shared nodes total  : ${sharedIntroduced} (in ${astsWithSharing} ASTs)`);
  if (failures > 0) {
    console.log('RESULT: FAIL');
    process.exit(1);
  }
  console.log('RESULT: PASS');
}

main();
