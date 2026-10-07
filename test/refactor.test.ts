import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  forEachNode,
  type Expr,
  type NodeId,
} from '../src/ast.ts';
import { resolve, type S } from '../src/resolve.ts';
import { freeVarsOf } from '../src/freevars.ts';
import { extractFunction, type ExtractionOk } from '../src/extract.ts';
import { evaluate, evaluateProgram, type Tracer, type Value } from '../src/interp.ts';
import { alphaRenameExpr } from '../src/alpha.ts';
import { genExpr, mulberry32 } from '../src/gen.ts';

// ---- surface builders ----------------------------------------------------
const num = (v: number): S => ({ k: 'num', v });
const bool = (v: boolean): S => ({ k: 'bool', v });
const v = (n: string): S => ({ k: 'var', n });
const lam = (p: string, b: S): S => ({ k: 'lam', p, b });
const app = (f: S, ...a: S[]): S => ({ k: 'app', f, a });
const let_ = (n: string, val: S, b: S): S => ({ k: 'let', n, v: val, b });
const add = (l: S, r: S): S => ({ k: 'bin', op: 'add', l, r });
const mul = (l: S, r: S): S => ({ k: 'bin', op: 'mul', l, r });
const ret = (x: S): S => ({ k: 'ret', v: x });

function allNodes(root: Expr): Expr[] {
  const out: Expr[] = [];
  forEachNode(root, (e) => out.push(e));
  return out;
}

function mustExtract(root: Expr, id: NodeId): ExtractionOk {
  const r = extractFunction(root, id);
  if (!r.ok) throw new Error(`expected extraction to succeed: ${r.reason}`);
  return r;
}

// ---- 1. captured free vars become params in first-occurrence order -------
test('capture order follows first occurrence', () => {
  const { root } = resolve(let_('x', num(1), let_('y', num(2), add(v('y'), mul(v('x'), v('y'))))));
  const target = allNodes(root).find((e) => e.kind === 'bin' && e.op === 'add')!;
  const r = mustExtract(root, target.id);
  assert.deepEqual(
    r.params.map((p) => p.name),
    ['y', 'x'],
  );
  // call site passes arguments in the same order
  assert.deepEqual(r.callSite.argRefs, r.params.map((p) => p.bind));
  assert.equal(evaluateProgram(r.program), evaluate(root)); // 2 + 1*2 = 4
});

// ---- 2. local bindings and shadowed variables do not leak ----------------
test('inner bindings never become parameters', () => {
  const src = let_('x', num(1), let_('x', num(2), add(v('x'), v('y'))));
  const { root, free } = resolve(src);
  const yBind = free.find((b) => b.name === 'y')!.bind;

  // extract the inner let: its own binding of x must not leak
  const innerLet = allNodes(root).filter((e) => e.kind === 'let')[1];
  const r1 = mustExtract(root, innerLet.id);
  assert.deepEqual(
    r1.params.map((p) => p.bind),
    [yBind],
  );

  // extract `x + y`: x here refers to the INNER let, so it is captured —
  // but with the inner binder's identity, never the outer same-name one
  const addNode = allNodes(root).find((e) => e.kind === 'bin')!;
  const r2 = mustExtract(root, addNode.id);
  const innerBinder = (innerLet as Extract<Expr, { kind: 'let' }>).binder;
  const outerBinder = (allNodes(root).filter((e) => e.kind === 'let')[0] as Extract<
    Expr,
    { kind: 'let' }
  >).binder;
  assert.deepEqual(
    r2.params.map((p) => p.bind),
    [innerBinder.bind, yBind],
  );
  assert.notEqual(innerBinder.bind, outerBinder.bind);
  assert.equal(evaluateProgram(r2.program, new Map([[yBind, 40]])), 42);
});

// ---- 3. captured closures keep their identity ----------------------------
test('captured closure identity is preserved across the call boundary', () => {
  const src = let_(
    'f',
    lam('x', add(v('x'), num(1))),
    let_('a', num(10), add(app(v('f'), v('a')), app(v('f'), num(20)))),
  );
  const { root } = resolve(src);
  const fLam = allNodes(root).find((e) => e.kind === 'lam')!;
  const firstApp = allNodes(root).find((e) => e.kind === 'app')!;

  const r = mustExtract(root, firstApp.id);
  assert.deepEqual(
    r.params.map((p) => p.name),
    ['f', 'a'],
  );

  const t1: Tracer = { events: [] };
  const t2: Tracer = { events: [] };
  const before = evaluate(root, new Map(), t1);
  const after = evaluateProgram(r.program, new Map(), t2);
  assert.equal(before, 32);
  assert.equal(after, before);

  for (const t of [t1, t2]) {
    const allocs = t.events.filter((e) => e.type === 'alloc' && e.defNode === fLam.id);
    const applies = t.events.filter((e) => e.type === 'apply' && e.defNode === fLam.id);
    assert.equal(allocs.length, 1, 'the captured closure is allocated exactly once');
    assert.ok(applies.length >= 2, 'closure applied inside and outside the extraction');
    const ids = new Set(applies.map((e) => (e.type === 'apply' ? e.closureId : -1)));
    assert.equal(ids.size, 1, 'same closure object flows through the extracted function');
  }
});

// ---- 4. return control boundaries ----------------------------------------
test('extraction across a return boundary is rejected', () => {
  const src = app(lam('x', ret(add(v('x'), num(1)))), num(5));
  const { root } = resolve(src);
  const retNode = allNodes(root).find((e) => e.kind === 'return')!;
  const r = extractFunction(root, retNode.id);
  assert.equal(r.ok, false);
  if (!r.ok) assert.match(r.reason, /return/);
});

test('return fully enclosed by a lambda inside the subtree is accepted', () => {
  const src = app(lam('x', ret(add(v('x'), num(1)))), num(5));
  const { root } = resolve(src);
  const lamNode = allNodes(root).find((e) => e.kind === 'lam')!;
  const r = mustExtract(root, lamNode.id);
  assert.equal(evaluateProgram(r.program), 6);
  assert.equal(evaluate(root), 6);
});

test('extracting under a return keeps the return in the root', () => {
  const src = app(lam('x', ret(add(v('x'), num(1)))), num(5));
  const { root } = resolve(src);
  const addNode = allNodes(root).find((e) => e.kind === 'bin')!;
  const r = mustExtract(root, addNode.id);
  assert.equal(evaluateProgram(r.program), evaluate(root));
});

// ---- 5. source map --------------------------------------------------------
test('source map covers every original node exactly once', () => {
  const src = let_('x', num(1), let_('y', num(2), add(v('x'), mul(v('y'), v('x')))));
  const { root } = resolve(src);
  const target = allNodes(root).find((e) => e.kind === 'bin')!;
  const r = mustExtract(root, target.id);

  const origIds = allNodes(root).map((e) => e.id);
  assert.equal(r.sourceMap.size, origIds.length);
  for (const id of origIds) assert.ok(r.sourceMap.has(id), `node ${id} mapped`);

  const movedIds = new Set(allNodes(target).map((e) => e.id));
  for (const id of origIds) {
    const dst = r.sourceMap.get(id)!;
    if (movedIds.has(id)) {
      assert.equal(dst.where, 'function');
      if (dst.where === 'function') assert.equal(dst.fn, r.fn.name);
    } else {
      assert.equal(dst.where, 'root');
    }
  }
  assert.equal(r.callSite.replaces, target.id);
  // the call site exists in the new root and moved nodes exist in the function
  assert.ok(allNodes(r.program.root).some((e) => e.id === r.callSite.id));
  for (const id of movedIds) {
    assert.ok(allNodes(r.fn.body).some((e) => e.id === id));
  }
});

// ---- 6. independent interpretation of both trees must agree --------------
test('refactoring preserves semantics on random programs', () => {
  let successes = 0;
  let rejections = 0;
  for (let seed = 0; seed < 300; seed++) {
    const rng = mulberry32(seed);
    const ty = rng() < 0.5 ? 'num' : 'bool';
    const env = [
      { name: 'fa', ty: 'num' as const },
      { name: 'fb', ty: 'num' as const },
      { name: 'fc', ty: 'bool' as const },
    ];
    const src = genExpr(rng, ty, env, 4, null);
    const { root, free } = resolve(src);
    const vals = new Map(
      free.map((b) => [b.bind, b.name === 'fc' ? rng() < 0.5 : Math.floor(rng() * 10)] as const),
    ) as Map<number, Value>;

    const before = evaluate(root, vals);
    const ids = allNodes(root).map((e) => e.id);
    for (let attempt = 0; attempt < 8; attempt++) {
      const target = ids[Math.floor(rng() * ids.length)];
      const r = extractFunction(root, target);
      if (!r.ok) {
        rejections++;
        assert.match(r.reason, /return/);
        continue;
      }
      successes++;
      const after = evaluateProgram(r.program, vals);
      assert.deepEqual(after, before, `seed ${seed} target ${target}`);
      break;
    }
  }
  assert.ok(successes >= 100, `enough successful extractions (got ${successes})`);
  assert.ok(rejections >= 0);
});

// ---- 7. random alpha-renaming must not change the parameter set ----------
test('alpha-renaming preserves the captured parameter set', () => {
  for (let seed = 0; seed < 100; seed++) {
    const rng = mulberry32(10_000 + seed);
    const src = genExpr(rng, 'num', [{ name: 'fa', ty: 'num' }], 4, null);
    const { root, free } = resolve(src);
    const vals = new Map(free.map((b) => [b.bind, Math.floor(rng() * 10)] as const));

    const ids = allNodes(root).map((e) => e.id);
    const target = ids[Math.floor(rng() * ids.length)];
    const r1 = extractFunction(root, target);

    const renamed = alphaRenameExpr(root, rng); // tiny pool => name collisions
    const r2 = extractFunction(renamed, target);

    assert.equal(r1.ok, r2.ok);
    if (r1.ok && r2.ok) {
      assert.deepEqual(
        r1.params.map((p) => p.bind),
        r2.params.map((p) => p.bind),
        `parameter BindIds identical after renaming (seed ${seed})`,
      );
      assert.deepEqual(evaluateProgram(r2.program, vals), evaluateProgram(r1.program, vals));
    }
    // free-variable analysis itself is renaming-invariant
    const node1 = allNodes(root).find((e) => e.id === target)!;
    const node2 = allNodes(renamed).find((e) => e.id === target)!;
    assert.deepEqual(
      freeVarsOf(node1).map((b) => b.bind),
      freeVarsOf(node2).map((b) => b.bind),
    );
  }
});
