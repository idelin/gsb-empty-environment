import {
  CmpOp, Expr, Row, Tri, Value, and, andTable, boolOf, cmp, compile, field, filterRows,
  interpret, isNotNull, isNull, lit, not, notTable, or, orTable,
} from '../src';
import { EvalResult } from '../src';
import { nodeAt, prune, sameNode } from './oracle';

let failures = 0;
let checks = 0;

function check(cond: boolean, label: string): void {
  checks++;
  if (!cond) {
    failures++;
    console.error(`FAIL: ${label}`);
  }
}

function expectTri(result: EvalResult, expected: Tri, label: string): void {
  check(result.ok && result.value === expected,
    `${label}: expected ${expected}, got ${JSON.stringify(result)}`);
}

function expectError(result: EvalResult, path: string, label: string): void {
  check(!result.ok && result.error.path === path,
    `${label}: expected error at ${path}, got ${JSON.stringify(result)}`);
}

const triLit = (t: Tri): Expr =>
  boolOf(lit(t === 'unknown' ? null : t === 'true'));

function truthTables(): void {
  const tris: Tri[] = ['true', 'false', 'unknown'];
  for (const a of tris) {
    for (const b of tris) {
      const eAnd = and(triLit(a), triLit(b));
      const eOr = or(triLit(a), triLit(b));
      expectTri(interpret(eAnd, {}), andTable[a][b], `interpret AND ${a} ${b}`);
      expectTri(compile(eAnd)({}), andTable[a][b], `compile AND ${a} ${b}`);
      expectTri(interpret(eOr, {}), orTable[a][b], `interpret OR ${a} ${b}`);
      expectTri(compile(eOr)({}), orTable[a][b], `compile OR ${a} ${b}`);
    }
    const eNot = not(triLit(a));
    expectTri(interpret(eNot, {}), notTable[a], `interpret NOT ${a}`);
    expectTri(compile(eNot)({}), notTable[a], `compile NOT ${a}`);
  }
}

function comparisons(): void {
  expectTri(interpret(cmp('eq', field('missing'), lit(1)), {}), 'unknown', 'missing field cmp');
  expectTri(interpret(cmp('lt', field('n'), lit(1)), { n: null }), 'unknown', 'null field cmp');
  expectTri(interpret(cmp('eq', lit(null), lit(null)), {}), 'unknown', 'null literal cmp');
  expectTri(interpret(cmp('eq', field('a'), lit(1)), { a: 1 }), 'true', 'eq number');
  expectTri(interpret(cmp('ne', field('a'), lit('x')), { a: 'y' }), 'true', 'ne string');
  expectTri(interpret(cmp('lt', lit('a'), lit('b')), {}), 'true', 'lt string');
  expectTri(interpret(cmp('gte', lit(2), lit(2)), {}), 'true', 'gte number');
  expectError(interpret(cmp('lt', lit(1), lit('x')), {}), '$', 'lt number vs string');
  expectError(interpret(cmp('eq', lit(1), lit(true)), {}), '$', 'eq number vs boolean');
  expectError(interpret(cmp('gt', lit(true), lit(false)), {}), '$', 'gt boolean vs boolean');
  expectError(compile(cmp('lte', lit('a'), lit(1)))({}), '$', 'compiled lte string vs number');
}

function nullTests(): void {
  expectTri(interpret(isNull(field('missing')), {}), 'true', 'isNull missing');
  expectTri(interpret(isNull(field('n')), { n: null }), 'true', 'isNull null');
  expectTri(interpret(isNull(field('a')), { a: 0 }), 'false', 'isNull value');
  expectTri(interpret(isNotNull(field('a')), { a: false }), 'true', 'isNotNull false value');
  expectTri(interpret(isNotNull(field('n')), { n: null }), 'false', 'isNotNull null');
  expectTri(compile(isNull(field('missing')))({}), 'true', 'compiled isNull missing');
}

function errorPositions(): void {
  const errA = cmp('lt', lit(1), lit('x'));
  const errB = cmp('gt', lit(true), lit(1));
  const good = cmp('eq', lit(1), lit(1));
  const goodFalse = cmp('eq', lit(1), lit(2));

  expectError(interpret(and(errA, errB), {}), '$.left', 'leftmost of two errors');
  expectError(interpret(and(good, and(good, errB)), {}), '$.right.right', 'nested right error');
  expectError(interpret(not(or(good, errA)), {}), '$.arg.right', 'error under not/or');
  expectError(compile(and(errA, errB))({}), '$.left', 'compiled leftmost of two errors');
  expectError(compile(not(or(goodFalse, errA)))({}), '$.arg.right', 'compiled error under not/or');
  expectTri(compile(not(or(good, errA)))({}), 'false', 'compiled not/or short-circuits dead error');

  const suppressed = or(boolOf(lit(true)), errA);
  expectError(interpret(suppressed, {}), '$.right', 'interpreter sees error in dead branch');
  expectTri(compile(suppressed)({}), 'true', 'compiler short-circuits dead error branch');

  const suppressedAnd = and(boolOf(lit(false)), errB);
  expectError(interpret(suppressedAnd, {}), '$.right', 'interpreter AND dead branch error');
  expectTri(compile(suppressedAnd)({}), 'false', 'compiler AND short-circuits');
}

function shortCircuitDoesNotEvaluate(): void {
  let accesses = 0;
  const row: Row = {};
  Object.defineProperty(row, 'bomb', {
    get() { accesses++; return 1; },
    enumerable: true,
  });

  const p1 = compile(and(boolOf(lit(false)), cmp('eq', field('bomb'), lit(1))));
  expectTri(p1(row), 'false', 'AND false short-circuits');
  check(accesses === 0, `AND skipped right branch (accesses=${accesses})`);

  const p2 = compile(or(boolOf(lit(true)), cmp('eq', field('bomb'), lit(1))));
  expectTri(p2(row), 'true', 'OR true short-circuits');
  check(accesses === 0, `OR skipped right branch (accesses=${accesses})`);

  const p3 = compile(and(boolOf(lit(true)), cmp('eq', field('bomb'), lit(1))));
  expectTri(p3(row), 'true', 'AND true evaluates right');
  check(accesses === 1, `AND evaluated right branch once (accesses=${accesses})`);

  let falseAccesses = 0;
  const row2: Row = {};
  Object.defineProperty(row2, 'flag', {
    get() { falseAccesses++; return false; },
    enumerable: true,
  });
  const p4 = compile(or(boolOf(field('flag')), boolOf(lit(true))));
  expectTri(p4(row2), 'true', 'OR false left evaluates right');
  check(falseAccesses === 1, `OR evaluated left field once (accesses=${falseAccesses})`);
}

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomExpr(rand: () => number, depth: number): Expr {
  const pick = <T>(xs: T[]): T => xs[Math.floor(rand() * xs.length)]!;
  const literalValues: Value[] = [0, 1, 2, 'x', 'y', true, false, null];
  const operand = () =>
    rand() < 0.5 ? field(pick(['a', 'b'])) : lit(pick(literalValues));
  if (depth <= 0) {
    const leaf = rand();
    if (leaf < 0.45) return cmp(pick(['eq', 'ne', 'lt', 'lte', 'gt', 'gte'] as CmpOp[]), operand(), operand());
    if (leaf < 0.7) return isNull(operand());
    if (leaf < 0.8) return isNotNull(operand());
    return boolOf(operand());
  }
  const r = rand();
  if (r < 0.35) return and(randomExpr(rand, depth - 1), randomExpr(rand, depth - 1));
  if (r < 0.7) return or(randomExpr(rand, depth - 1), randomExpr(rand, depth - 1));
  if (r < 0.8) return not(randomExpr(rand, depth - 1));
  return randomExpr(rand, 0);
}

function crossCheck(): void {
  const states: (Value | undefined)[] = [undefined, null, 0, 1, 'x', true, false];
  const rows: Row[] = [];
  for (const a of states) for (const b of states) rows.push({ a, b });

  const rand = mulberry32(20261006);
  let pairs = 0;
  let bothOk = 0;
  let bothErr = 0;
  let skippedErr = 0;

  for (let i = 0; i < 4000; i++) {
    const expr = randomExpr(rand, 4);
    const pred = compile(expr);
    for (const row of rows) {
      pairs++;
      const ir = interpret(expr, row);
      const pruned = prune(expr, row);
      const pr = interpret(pruned, row);
      const cr = pred(row);
      const label = `expr=${JSON.stringify(expr)} row=${JSON.stringify(row)}`;
      if (pr.ok && cr.ok) {
        bothOk++;
        check(pr.value === cr.value,
          `value mismatch vs pruned interpreter: pruned=${pr.value} compiled=${cr.value} ${label}`);
      } else if (!pr.ok && !cr.ok) {
        bothErr++;
        check(pr.error.path === cr.error.path && pr.error.message === cr.error.message,
          `error mismatch vs pruned interpreter: pruned=${JSON.stringify(pr.error)} compiled=${JSON.stringify(cr)} ${label}`);
      } else if (!pr.ok && cr.ok) {
        check(false, `compiler hid a live error: pruned=${JSON.stringify(pr.error)} compiled=${JSON.stringify(cr)} ${label}`);
      } else {
        check(false, `compiler invented error: compiled=${JSON.stringify(cr)} ${label}`);
      }
      if (ir.ok) {
        check(pr.ok && pr.value === ir.value,
          `pruning changed error-free result: interpreter=${ir.value} pruned=${JSON.stringify(pr)} ${label}`);
      } else if (pr.ok) {
        skippedErr++;
      } else {
        const liveError = sameNode(nodeAt(pruned, ir.error.path), nodeAt(expr, ir.error.path));
        if (liveError) {
          check(ir.error.path === pr.error.path,
            `live error path mismatch: interpreter=${ir.error.path} pruned=${pr.ok ? 'ok' : pr.error.path} ${label}`);
        }
      }
    }
  }
  console.log(`cross-check: ${pairs} (expr,row) pairs, both-ok=${bothOk}, both-error=${bothErr}, skipped-error=${skippedErr}`);
  check(skippedErr > 0, 'short-circuit error suppression was exercised');
  check(bothErr > 0, 'shared error cases were exercised');
  check(bothOk > 0, 'value agreement cases were exercised');
}

function filtering(): void {
  const pred = compile(and(cmp('gte', field('age'), lit(18)), isNotNull(field('name'))));
  const rows: Row[] = [
    { age: 20, name: 'a' },
    { age: 15, name: 'b' },
    { age: 30 },
    { age: null, name: 'd' },
    { age: 40, name: 'e' },
  ];
  const outcome = filterRows(rows, pred);
  check(outcome.ok && outcome.rows.length === 2 &&
    outcome.rows[0]!.name === 'a' && outcome.rows[1]!.name === 'e',
    `filter keeps only true rows: ${JSON.stringify(outcome)}`);

  const badPred = compile(cmp('lt', field('v'), lit(1)));
  const bad = filterRows([{ v: 0 }, { v: 'oops' }], badPred);
  check(!bad.ok && bad.rowIndex === 1 && bad.error.path === '$',
    `filter reports error row: ${JSON.stringify(bad)}`);
}

truthTables();
comparisons();
nullTests();
errorPositions();
shortCircuitDoesNotEvaluate();
crossCheck();
filtering();

console.log(`${checks} checks, ${failures} failures`);
if (failures > 0) process.exitCode = 1;
