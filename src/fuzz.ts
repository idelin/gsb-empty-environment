// Differential test: for each program, the interpreter must produce the
// same value-or-error and the same side-effect trace on the original and
// on the optimized AST.
//
// Usage: bun run src/fuzz.ts [iterations]

import { Expr, render, lit, pure, tick, ifE, and, or } from './ast';
import { run, RunResult } from './interp';
import { optimize } from './opt';
import { genProgram, mulberry32 } from './gen';

function sameOutcome(a: RunResult, b: RunResult): boolean {
  if (a.error !== b.error) return false;
  if (a.error === null && !Object.is(a.value, b.value)) return false;
  return a.trace.length === b.trace.length && a.trace.every((t, i) => t === b.trace[i]);
}

function countLets(e: Expr): number {
  let n = e.tag === 'let' ? 1 : 0;
  switch (e.tag) {
    case 'pure':
    case 'eff':
      e.args.forEach((a) => { n += countLets(a); });
      break;
    case 'if':
      n += countLets(e.cond) + countLets(e.then) + countLets(e.els);
      break;
    case 'and':
    case 'or':
      n += countLets(e.lhs) + countLets(e.rhs);
      break;
    case 'let':
      n += countLets(e.value) + countLets(e.body);
      break;
  }
  return n;
}

// Hand-picked edge cases targeting each safety invariant.
const fixed: Array<[string, Expr]> = [
  ['fallible dup in strict position',
    pure('add', pure('div', lit(1), lit(0)), pure('div', lit(1), lit(0)))],
  ['fallible dup must not cross an effect',
    pure('sub',
      pure('add', tick(lit(1)), pure('div', lit(1), lit(0))),
      pure('div', lit(1), lit(0)))],
  ['fallible dup must not escape a lazy branch',
    pure('add',
      ifE(lit(0), pure('div', lit(1), lit(0)), lit(5)),
      pure('div', lit(1), lit(0)))],
  ['fallible dup across short-circuit rhs',
    pure('add',
      and(lit(0), pure('div', lit(1), lit(0))),
      pure('div', lit(1), lit(0)))],
  ['infallible dup may hoist out of both branches',
    ifE(lit(1), pure('add', lit(1), lit(2)), pure('add', lit(1), lit(2)))],
  ['infallible dup may cross an effect',
    pure('add',
      tick(pure('add', lit(2), lit(3))),
      pure('mul', pure('add', lit(2), lit(3)), pure('add', lit(2), lit(3))))],
  ['effect order preserved',
    pure('add', tick(lit(1)), tick(lit(2)))],
  ['effect not deduplicated',
    pure('add', tick(lit(7)), tick(lit(7)))],
  ['lazy effect never runs',
    or(lit(1), tick(lit(5)))],
  ['error message identity after dedup',
    pure('add',
      pure('mod', lit(3), lit(0)),
      pure('mul', pure('mod', lit(3), lit(0)), tick(lit(9))))],
];

function check(program: Expr, label: string): { problem: string | null; lets: number } {
  let optimized: Expr;
  try {
    optimized = optimize(program);
  } catch (err) {
    return { problem: `optimizer crashed on ${label}: ${(err as Error).stack}`, lets: 0 };
  }
  const before = run(program);
  const after = run(optimized);
  const problem = sameOutcome(before, after)
    ? null
    : [
        `MISMATCH on ${label}`,
        `  program:   ${render(program)}`,
        `  optimized: ${render(optimized)}`,
        `  before:    value=${before.value} error=${before.error} trace=[${before.trace.join(', ')}]`,
        `  after:     value=${after.value} error=${after.error} trace=[${after.trace.join(', ')}]`,
      ].join('\n');
  return { problem, lets: countLets(optimized) };
}

const iterations = Number(process.argv[2] ?? 5000);
let failures = 0;
let sharedPrograms = 0;
let letsIntroduced = 0;

for (const [label, program] of fixed) {
  const { problem } = check(program, `fixed:${label}`);
  if (problem) {
    console.error(problem);
    failures++;
  }
}

for (let i = 0; i < iterations; i++) {
  const program = genProgram(mulberry32(i + 1));
  const { problem, lets } = check(program, `seed=${i + 1}`);
  if (lets > 0) {
    sharedPrograms++;
    letsIntroduced += lets;
  }
  if (problem) {
    console.error(problem);
    if (++failures > 5) break;
  }
}

console.log(
  `checked ${fixed.length} fixed cases + ${iterations} random programs; ` +
  `${sharedPrograms} programs optimized (${letsIntroduced} lets introduced); ` +
  `${failures} failure(s)`,
);
process.exit(failures === 0 ? 0 : 1);
