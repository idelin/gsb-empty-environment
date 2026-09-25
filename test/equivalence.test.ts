import assert from 'node:assert/strict';
import test from 'node:test';
import { compile } from '../src/dag.ts';
import { PolicyEngine } from '../src/engine.ts';
import {
  and,
  attrsOf,
  eq,
  evalExpr,
  inSet,
  ne,
  not,
  or,
  range,
  type Expr,
} from '../src/expr.ts';
import { partialEval } from '../src/partial.ts';
import { InMemoryAttrStore } from '../src/store.ts';

// A policy mixing finite sets, integer ranges, equality and nested booleans.
const policy: Expr = or(
  and(inSet('a', [1, 2, 3]), range('b', 2, null)),
  and(not(eq('a', 0)), inSet('c', [0, 4])),
  and(range('a', 0, 1), or(eq('b', 3), ne('c', 2))),
);

const ATTRS = ['a', 'b', 'c'] as const;
const DOMAIN: (number | undefined)[] = [undefined, 0, 1, 2, 3, 4];

function* assignments(): Generator<Record<string, number | undefined>> {
  for (const a of DOMAIN)
    for (const b of DOMAIN)
      for (const c of DOMAIN) yield { a, b, c };
}

test('finite-domain truth enumeration: DAG engine == naive reference', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store, { cache: false });
  engine.setRule('p', policy);

  let checked = 0;
  for (const combo of assignments()) {
    for (const attr of ATTRS) {
      const v = combo[attr];
      if (v === undefined) store.remove('s', attr);
      else store.set('s', attr, v);
    }
    const expected = evalExpr(policy, (attr) => combo[attr]);
    const got = await engine.evaluate('p', 's');
    assert.equal(got.status, 'ok');
    assert.equal(
      got.decision,
      expected,
      `mismatch at ${JSON.stringify(combo)}: dag=${got.decision} naive=${expected}`,
    );
    checked++;
  }
  assert.equal(checked, DOMAIN.length ** ATTRS.length);
});

test('finite-domain truth enumeration: partial evaluation preserves semantics', () => {
  const subsets: (typeof ATTRS[number])[][] = [
    [],
    ['a'],
    ['b'],
    ['c'],
    ['a', 'b'],
    ['a', 'c'],
    ['b', 'c'],
    ['a', 'b', 'c'],
  ];
  let checked = 0;
  for (const combo of assignments()) {
    const full = evalExpr(policy, (attr) => combo[attr]);
    for (const knownAttrs of subsets) {
      const known = new Map<string, unknown>();
      for (const k of knownAttrs) known.set(k, combo[k]);
      const residual = partialEval(policy, known);
      // Residual may only reference attributes that were not known.
      for (const dep of attrsOf(residual)) assert.ok(!known.has(dep), `residual leaks ${dep}`);
      const viaResidual = evalExpr(residual, (attr) => combo[attr]);
      assert.equal(
        viaResidual,
        full,
        `partial-eval mismatch at ${JSON.stringify(combo)} known=${knownAttrs.join(',')}`,
      );
      checked++;
    }
  }
  assert.ok(checked > 0);
});

test('compilation shares identical subtrees in the DAG', () => {
  const expr = or(eq('a', 1), not(eq('a', 1)));
  const dag = compile(expr);
  assert.equal(dag.nodes.length, 3); // shared leaf + NOT + OR
  assert.deepEqual(dag.root.deps, ['a']);
  const [leaf, notNode] = dag.nodes;
  assert.equal(notNode!.children[0]!.id, leaf!.id); // same node object reused
});

test('engine residual API matches partial evaluation over the same rule', async () => {
  const store = new InMemoryAttrStore();
  const engine = new PolicyEngine(store);
  engine.setRule('p', policy);
  const r = engine.residual('p', new Map([['a', 1]]));
  const expected = evalExpr(
    partialEval(policy, new Map([['a', 1]])),
    () => undefined,
  );
  assert.equal(evalExpr(r.expr, () => undefined), expected);
});
