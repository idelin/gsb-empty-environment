// test/run.ts — unit tests + property-based verification of the extractor.

import { Expr, NodeId, findNode, nodeIds, parse, resolve, show } from '../src/core';
import { freeVars } from '../src/analyze';
import { ExtractError, extract } from '../src/extract';
import { Env, Value, apply, run, showValue } from '../src/interp';
import { alphaRename } from '../src/alpha';
import { Gen, genEnv, mulberry32 } from '../src/random';

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
  } catch (e) {
    failed++;
    console.error(`FAIL: ${name}\n  ${(e as Error).message}`);
  }
}

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

function parseResolve(src: string) {
  return resolve(parse(src));
}

function findWhere(root: Expr, pred: (e: Expr) => boolean): Expr {
  for (const id of nodeIds(root)) {
    const n = findNode(root, id)!;
    if (pred(n)) return n;
  }
  throw new Error('node not found');
}

// extensional equality: functions are compared by applying them to samples
function valuesEqual(a: Value, b: Value, rand: () => number, depth = 3): boolean {
  if (typeof a !== 'object' || typeof b !== 'object') return a === b;
  if (depth <= 0) return true;
  for (let i = 0; i < 3; i++) {
    const arg = Math.floor(rand() * 21) - 10;
    let ra: Value, rb: Value;
    try {
      ra = apply(a, arg);
    } catch {
      return false;
    }
    try {
      rb = apply(b, arg);
    } catch {
      return false;
    }
    if (!valuesEqual(ra, rb, rand, depth - 1)) return false;
  }
  return true;
}

// ---------------- unit tests ----------------

test('params follow first-occurrence order', () => {
  const { expr } = parseResolve('(let ((b 1)) (let ((a 2)) (+ b (+ a (+ b a)))))');
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === '+');
  const res = extract(expr, target.id);
  assert(res.params.map((p) => p.name).join(',') === 'b,a', `expected [b,a], got [${res.params.map((p) => p.name)}]`);
  assert(run(expr) === 6 && run(res.program) === 6, 'evaluation mismatch');
});

test('shadowed variable captured by inner binding identity, not name', () => {
  const { expr, globals } = parseResolve('(let ((x 1)) (let ((x 2)) (+ x z)))');
  const innerLet = findWhere(expr, (e) => e.k === 'let' && e.name === 'x' && e.value.k === 'lit' && e.value.v === 2);
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === '+');
  const res = extract(expr, target.id);
  assert(res.params.length === 2, `expected 2 params, got ${res.params.length}`);
  assert(res.params[0].capturedFrom === (innerLet as Expr & { k: 'let' }).bind, 'x must capture the INNER let binding');
  assert(res.params[1].capturedFrom === globals.get('z')!, 'z must capture the global');
  const env: Env = new Map([[globals.get('z')!, 10]]);
  assert(run(expr, env) === 12 && run(res.program, env) === 12, 'evaluation mismatch');
});

test('locals bound inside the subtree do not leak into params', () => {
  const { expr, globals } = parseResolve('(let ((q 0)) (let ((w 3)) (+ w z)))');
  const inner = findWhere(expr, (e) => e.k === 'let' && e.name === 'w');
  const res = extract(expr, inner.id);
  assert(res.params.length === 1 && res.params[0].name === 'z', `expected [z], got [${res.params.map((p) => p.name)}]`);
  const env: Env = new Map([[globals.get('z')!, 5]]);
  assert(run(expr, env) === 8 && run(res.program, env) === 8, 'evaluation mismatch');
});

test('lambda-bound vars inside subtree are not captured', () => {
  const { expr, globals } = parseResolve('((lambda (x) (+ x y)) 4)');
  const lam = findWhere(expr, (e) => e.k === 'lam');
  const res = extract(expr, lam.id);
  assert(res.params.length === 1 && res.params[0].name === 'y', `expected [y], got [${res.params.map((p) => p.name)}]`);
  const env: Env = new Map([[globals.get('y')!, 100]]);
  assert(run(expr, env) === 104 && run(res.program, env) === 104, 'evaluation mismatch');
});

test('captured closures preserve identity (same? f g where g aliases f)', () => {
  const src = '(let ((f (lambda (x) x))) (let ((g f)) (same? f g)))';
  const { expr } = parseResolve(src);
  assert(run(expr) === true, 'original should be true');
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === 'same?');
  const res = extract(expr, target.id);
  assert(res.params.map((p) => p.name).join(',') === 'f,g', 'params should be f,g');
  // if extraction rebuilt closures instead of passing originals, f and g
  // would become distinct objects and this would flip to false
  assert(run(res.program) === true, 'identity of captured closure not preserved');
});

test('call site passes original binding ids', () => {
  const { expr } = parseResolve('(let ((f (lambda (x) (+ x 1)))) (+ (f 1) (f 2)))');
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === '+' && e.args[0].k === 'app');
  const fBind = (findWhere(expr, (e) => e.k === 'let' && e.name === 'f') as Expr & { k: 'let' }).bind;
  const res = extract(expr, target.id);
  const call = findNode(res.program, res.callSite)!;
  // walk the app chain, collecting argument refs
  const argRefs: number[] = [];
  let cur = call;
  while (cur.k === 'app') {
    assert(cur.arg.k === 'var', 'call-site argument must be a variable');
    argRefs.push((cur.arg as Expr & { k: 'var' }).ref);
    cur = cur.fn;
  }
  assert(cur.k === 'var' && (cur as Expr & { k: 'var' }).ref === res.fnBind, 'call head must be the new function');
  assert(argRefs.length === 1 && argRefs[0] === fBind, `call site must pass original f binding, got ${argRefs}`);
});

test('reject extraction crossing a return control boundary', () => {
  const { expr } = parseResolve('((lambda (x) (return (+ x 1))) 5)');
  const ret = findWhere(expr, (e) => e.k === 'return');
  let threw = false;
  try {
    extract(expr, ret.id);
  } catch (e) {
    threw = e instanceof ExtractError;
  }
  assert(threw, 'extracting a bare return must be rejected');
});

test('allow extraction under a return, and of lambdas containing return', () => {
  const { expr } = parseResolve('((lambda (x) (return (+ x 1))) 5)');
  const plus = findWhere(expr, (e) => e.k === 'prim' && e.op === '+');
  const r1 = extract(expr, plus.id); // return node stays outside the extracted subtree
  assert(run(expr) === 6 && run(r1.program) === 6, 'evaluation mismatch under return');
  const lam = findWhere(expr, (e) => e.k === 'lam');
  const r2 = extract(expr, lam.id); // return is shadowed by the lambda inside the subtree
  assert(run(r2.program) === 6, 'evaluation mismatch for lambda-with-return');
});

test('source map links generated body back to original nodes', () => {
  const { expr } = parseResolve('(let ((x 1)) (+ x (* x 2)))');
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === '+');
  const res = extract(expr, target.id);
  const sm = res.sourceMap;
  assert(sm.target === target.id, 'target recorded');
  assert(findNode(res.program, res.callSite) !== undefined, 'call site present in program');
  // every copied body node maps back to a node inside the original target
  const targetIds = new Set(nodeIds(target));
  const fnValue = (res.program as Expr & { k: 'let' }).value;
  let body = fnValue;
  while (body.k === 'lam') body = body.body;
  for (const id of nodeIds(body)) {
    const orig = sm.originOf(id);
    assert(orig !== undefined && targetIds.has(orig), `generated node ${id} has no origin inside target`);
  }
  // reverse map: original target root maps to the copied body root
  assert(sm.generatedFrom(target.id).includes(body.id), 'reverse map missing body root');
});

test('extracting the root node works', () => {
  const { expr, globals } = parseResolve('(+ x 1)');
  const res = extract(expr, expr.id);
  const env: Env = new Map([[globals.get('x')!, 41]]);
  assert(run(res.program, env) === 42, 'evaluation mismatch');
});

test('extracting a missing node raises ExtractError', () => {
  const { expr } = parseResolve('(+ 1 2)');
  let threw = false;
  try {
    extract(expr, 999999);
  } catch (e) {
    threw = e instanceof ExtractError;
  }
  assert(threw, 'expected ExtractError');
});

test('alpha-renaming does not change the parameter set', () => {
  const { expr } = parseResolve('(let ((x 1)) (let ((y 2)) (let ((x 3)) (+ x (+ y z)))))');
  const target = findWhere(expr, (e) => e.k === 'prim' && e.op === '+');
  const rand = mulberry32(1234);
  for (let i = 0; i < 20; i++) {
    const renamed = alphaRename(expr, rand);
    const r1 = extract(expr, target.id);
    const r2 = extract(renamed, target.id);
    const s1 = r1.params.map((p) => p.capturedFrom).join(',');
    const s2 = r2.params.map((p) => p.capturedFrom).join(',');
    assert(s1 === s2, `param identity sets differ: ${s1} vs ${s2}`);
  }
});

// ---------------- property tests ----------------

function propertyTests(trials: number, seed0: number): void {
  let extractions = 0;
  let rejected = 0;
  for (let t = 0; t < trials; t++) {
    const rand = mulberry32(seed0 + t);
    const gen = new Gen(rand);
    const surface = gen.genExpr('num', 4, false);
    const { expr, globals } = resolve(surface);
    const env = genEnv(globals, gen.freeTypes, rand);

    let original: Value;
    try {
      original = run(expr, env);
    } catch (e) {
      throw new Error(`generated program failed to evaluate (seed ${seed0 + t}): ${show(expr)}\n${e}`);
    }

    for (const id of nodeIds(expr)) {
      let res;
      try {
        res = extract(expr, id);
      } catch (e) {
        if (e instanceof ExtractError) {
          rejected++;
          continue;
        }
        throw e;
      }
      extractions++;

      // 1. independent interpretation of original vs refactored tree
      const refactored = run(res.program, env);
      if (!valuesEqual(original, refactored, rand)) {
        throw new Error(
          `semantic mismatch (seed ${seed0 + t}, node ${id})\n` +
            `  original:   ${showValue(original)}\n` +
            `  refactored: ${showValue(refactored)}\n` +
            `  tree: ${show(expr)}`,
        );
      }

      // 2. random alpha-renaming must not change the parameter set
      const renamed = alphaRename(expr, rand);
      const res2 = extract(renamed, id);
      const p1 = res.params.map((p) => p.capturedFrom);
      const p2 = res2.params.map((p) => p.capturedFrom);
      if (p1.length !== p2.length || !p1.every((v, i) => v === p2[i])) {
        throw new Error(`alpha-renaming changed params (seed ${seed0 + t}, node ${id}): ${p1} vs ${p2}`);
      }
      const refactored2 = run(res2.program, env);
      if (!valuesEqual(original, refactored2, rand)) {
        throw new Error(`renamed refactored mismatch (seed ${seed0 + t}, node ${id})`);
      }
    }
  }
  console.log(`property tests: ${trials} programs, ${extractions} extractions verified, ${rejected} return-boundary rejections`);
}

test('freeVars: first-occurrence order with shadowing', () => {
  const { expr, globals } = parseResolve('(let ((x 1)) (+ x (+ y (let ((y 2)) (+ y x)))))');
  const fvs = freeVars(expr);
  assert(fvs.length === 1 && fvs[0].ref === globals.get('y')!, `expected only outer y free, got ${JSON.stringify(fvs)}`);
});

propertyTests(150, 42);

console.log(`\n${passed} unit tests passed, ${failed} failed`);
if (failed > 0) process.exit(1);
