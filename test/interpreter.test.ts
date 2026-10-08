import { test } from 'node:test';
import assert from 'node:assert/strict';
import { evaluate, I64_MAX } from '../src/interpreter.ts';
import { RuleService } from '../src/service.ts';
import type { Expr } from '../src/ast.ts';

const yieldLoop = (): Promise<void> =>
  new Promise((resolve) => {
    setImmediate(resolve);
  });

const int = (value: number | bigint | string): Expr => ({ kind: 'int', value });
const bool = (value: boolean): Expr => ({ kind: 'bool', value });
const v = (name: string): Expr => ({ kind: 'var', name });
const add = (left: Expr, right: Expr): Expr => ({ kind: 'arith', op: 'add', left, right });

test('short-circuit: unexecuted branches are not visited and not counted', async () => {
  // and(false, <missing var>) must not touch the second argument at all.
  const andAst: Expr = { kind: 'and', args: [bool(false), v('missing')] };
  const r1 = await evaluate({ ast: andAst, budget: { steps: 2, space: 0 } });
  assert.deepEqual(r1, { ok: true, value: false, stepsUsed: 2, spaceUsed: 0 });

  const orAst: Expr = { kind: 'or', args: [bool(true), v('missing')] };
  const r2 = await evaluate({ ast: orAst, budget: { steps: 2, space: 0 } });
  assert.deepEqual(r2, { ok: true, value: true, stepsUsed: 2, spaceUsed: 0 });

  // if: only the taken branch is visited (if + cond + then = 3 steps).
  const ifAst: Expr = { kind: 'if', cond: bool(true), then: int(1), else: v('missing') };
  const r3 = await evaluate({ ast: ifAst, budget: { steps: 3, space: 0 } });
  assert.deepEqual(r3, { ok: true, value: 1n, stepsUsed: 3, spaceUsed: 0 });

  // Proof the skipped branch would have failed had it been visited.
  const r4 = await evaluate({ ast: v('missing'), budget: { steps: 10, space: 0 } });
  assert.equal(r4.ok, false);
  if (!r4.ok) assert.equal(r4.error.kind, 'UndefinedVariable');
});

test('budget: exactly enough succeeds, one step short fails with AST path', async () => {
  const ast = add(int(1), int(2)); // 3 node visits: arith, left, right
  const ok = await evaluate({ ast, budget: { steps: 3, space: 0 } });
  assert.deepEqual(ok, { ok: true, value: 3n, stepsUsed: 3, spaceUsed: 0 });

  const short = await evaluate({ ast, budget: { steps: 2, space: 0 } });
  assert.equal(short.ok, false);
  if (!short.ok) {
    assert.equal(short.error.kind, 'StepsExhausted');
    assert.equal(short.error.path, '$/right'); // check fired before visiting right
    assert.equal(short.stepsUsed, 2);
  }
});

test('deep nesting: explicit stack evaluates 100k-deep AST without call-stack overflow', async () => {
  const depth = 100_000;
  let ast: Expr = int(1);
  for (let i = 0; i < depth; i++) ast = { kind: 'neg', arg: ast };
  const r = await evaluate({ ast, budget: { steps: depth + 1, space: 0 } });
  assert.deepEqual(r, { ok: true, value: 1n, stepsUsed: depth + 1, spaceUsed: 0 });
});

test('map/filter/fold: element reads cost steps, created arrays cost space', async () => {
  const xs = [1, 2, 3];
  const mapAst: Expr = { kind: 'map', list: v('xs'), param: 'x', body: add(v('x'), int(1)) };
  // steps: map(1) + var(1) + 3 * (read 1 + arith 1 + var 1 + int 1) = 14; space: 3
  const r1 = await evaluate({ ast: mapAst, variables: { xs }, budget: { steps: 14, space: 3 } });
  assert.deepEqual(r1, { ok: true, value: [2n, 3n, 4n], stepsUsed: 14, spaceUsed: 3 });

  // one step short
  const r2 = await evaluate({ ast: mapAst, variables: { xs }, budget: { steps: 13, space: 3 } });
  assert.equal(r2.ok, false);
  if (!r2.ok) {
    assert.equal(r2.error.kind, 'StepsExhausted');
    assert.ok(r2.error.path.startsWith('$/body'));
  }

  // one space slot short: fails before appending the 3rd element
  const r3 = await evaluate({ ast: mapAst, variables: { xs }, budget: { steps: 100, space: 2 } });
  assert.equal(r3.ok, false);
  if (!r3.ok) {
    assert.equal(r3.error.kind, 'SpaceExhausted');
    assert.equal(r3.error.path, '$');
  }

  const filterAst: Expr = {
    kind: 'filter',
    list: v('xs'),
    param: 'x',
    body: { kind: 'cmp', op: 'gt', left: v('x'), right: int(2) },
  };
  const r4 = await evaluate({ ast: filterAst, variables: { xs: [1, 2, 3, 4] }, budget: { steps: 100, space: 100 } });
  assert.deepEqual(r4, { ok: true, value: [3n, 4n], stepsUsed: 18, spaceUsed: 2 });

  const foldAst: Expr = {
    kind: 'fold',
    list: v('xs'),
    param: 'x',
    acc: 'acc',
    init: int(0),
    body: add(v('acc'), v('x')),
  };
  // steps: fold(1) + var(1) + init(1) + 3 * (read 1 + arith 1 + var 1 + var 1) = 15
  const r5 = await evaluate({ ast: foldAst, variables: { xs }, budget: { steps: 15, space: 0 } });
  assert.deepEqual(r5, { ok: true, value: 6n, stepsUsed: 15, spaceUsed: 0 });
});

test('errors carry AST paths: overflow, type error, missing variable, div by zero, forbidden node', async () => {
  const overflow = await evaluate({ ast: add(int(I64_MAX), int(1)), budget: { steps: 10, space: 0 } });
  assert.equal(overflow.ok, false);
  if (!overflow.ok) {
    assert.equal(overflow.error.kind, 'Overflow');
    assert.equal(overflow.error.path, '$');
  }

  const badLiteral = await evaluate({ ast: int('9223372036854775808'), budget: { steps: 10, space: 0 } });
  assert.equal(badLiteral.ok, false);
  if (!badLiteral.ok) assert.equal(badLiteral.error.kind, 'Overflow');

  const typeErr = await evaluate({ ast: add(int(1), bool(true)), budget: { steps: 10, space: 0 } });
  assert.equal(typeErr.ok, false);
  if (!typeErr.ok) {
    assert.equal(typeErr.error.kind, 'TypeError');
    assert.equal(typeErr.error.path, '$');
  }

  const missing = await evaluate({ ast: v('nope'), budget: { steps: 10, space: 0 } });
  assert.equal(missing.ok, false);
  if (!missing.ok) {
    assert.equal(missing.error.kind, 'UndefinedVariable');
    assert.equal(missing.error.path, '$');
  }

  const divZero = await evaluate({
    ast: { kind: 'arith', op: 'div', left: int(1), right: int(0) },
    budget: { steps: 10, space: 0 },
  });
  assert.equal(divZero.ok, false);
  if (!divZero.ok) assert.equal(divZero.error.kind, 'DivisionByZero');

  // eval / host calls / dynamic imports have no node kind; anything unknown is rejected.
  const callAst = { kind: 'call', name: 'eval', args: [] } as unknown as Expr;
  const forbidden = await evaluate({ ast: callAst, budget: { steps: 10, space: 0 } });
  assert.equal(forbidden.ok, false);
  if (!forbidden.ok) {
    assert.equal(forbidden.error.kind, 'ForbiddenNode');
    assert.equal(forbidden.error.path, '$');
  }

  const nested = await evaluate({
    ast: { kind: 'and', args: [bool(true), { kind: 'import' } as unknown as Expr] },
    budget: { steps: 10, space: 0 },
  });
  assert.equal(nested.ok, false);
  if (!nested.ok) {
    assert.equal(nested.error.kind, 'ForbiddenNode');
    assert.equal(nested.error.path, '$/args/1');
  }
});

const heavyMap = (xs: number[], negDepth: number): Expr => {
  let body: Expr = v('x');
  for (let i = 0; i < negDepth; i++) body = { kind: 'neg', arg: body };
  return { kind: 'map', list: v('xs'), param: 'x', body };
};

test('cancellation releases tenant quota so it can be reused', async () => {
  const svc = new RuleService({ sliceSteps: 1 });
  svc.setTenantSpaceLimit('t1', 10);

  // Slow request: each element body takes ~31 steps, so it spans many slices.
  const h1 = svc.submit({
    tenantId: 't1',
    ast: heavyMap([1, 2, 3, 4, 5, 6, 7, 8], 30),
    variables: { xs: [1, 2, 3, 4, 5, 6, 7, 8] },
    budget: { steps: 100_000, space: 100 },
  });
  while (svc.tenantSpaceInUse('t1') === 0) await yieldLoop();
  h1.cancel();

  const r1 = await h1.promise;
  assert.equal(r1.ok, false);
  if (!r1.ok) assert.equal(r1.error.kind, 'Cancelled');
  assert.equal(svc.tenantSpaceInUse('t1'), 0, 'cancelled request must release its reservation');

  // The freed quota is immediately reusable: this needs all 10 slots.
  const h2 = svc.submit({
    tenantId: 't1',
    ast: heavyMap([1, 2, 3, 4, 5, 6, 7, 8, 9, 10], 0),
    variables: { xs: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10] },
    budget: { steps: 100_000, space: 100 },
  });
  const r2 = await h2.promise;
  assert.equal(r2.ok, true);
  if (r2.ok) {
    assert.equal(r2.spaceUsed, 10);
    assert.deepEqual(r2.value, [1n, 2n, 3n, 4n, 5n, 6n, 7n, 8n, 9n, 10n]);
  }
  assert.equal(svc.tenantSpaceInUse('t1'), 0);
});

test('failed requests release tenant quota too', async () => {
  const svc = new RuleService({ sliceSteps: 1 });
  svc.setTenantSpaceLimit('t2', 5);

  // Appends 1 slot for element 1, then type-errors on element 2.
  const ast: Expr = {
    kind: 'map',
    list: v('xs'),
    param: 'x',
    body: {
      kind: 'if',
      cond: { kind: 'cmp', op: 'gt', left: v('x'), right: int(1) },
      then: add(v('x'), bool(true)),
      else: v('x'),
    },
  };
  const h = svc.submit({ tenantId: 't2', ast, variables: { xs: [1, 2, 3] }, budget: { steps: 1000, space: 100 } });
  const r = await h.promise;
  assert.equal(r.ok, false);
  if (!r.ok) {
    assert.equal(r.error.kind, 'TypeError');
    assert.equal(r.error.path, '$/body/then');
  }
  assert.equal(svc.tenantSpaceInUse('t2'), 0, 'failed request must release its reservation');
});

test('two tenants interleaved: independent counters and cancellation', async () => {
  const svc = new RuleService({ sliceSteps: 2 });
  svc.setTenantSpaceLimit('a', 4);
  svc.setTenantSpaceLimit('b', 4);

  const xs = [1, 2, 3, 4];
  const hA = svc.submit({
    tenantId: 'a',
    ast: heavyMap(xs, 20),
    variables: { xs },
    budget: { steps: 100_000, space: 100 },
  });
  const hB = svc.submit({
    tenantId: 'b',
    ast: { kind: 'map', list: v('xs'), param: 'x', body: add(v('x'), int(10)) },
    variables: { xs },
    budget: { steps: 100_000, space: 100 },
  });

  hA.cancel(); // must not affect tenant b
  const [rA, rB] = await Promise.all([hA.promise, hB.promise]);

  assert.equal(rA.ok, false);
  if (!rA.ok) assert.equal(rA.error.kind, 'Cancelled');

  // Tenant b ran to completion with its own independent counters:
  // steps: map(1) + var(1) + 4 * (read 1 + arith 1 + var 1 + int 1) = 18; space: 4
  assert.deepEqual(rB, {
    ok: true,
    value: [11n, 12n, 13n, 14n],
    stepsUsed: 18,
    spaceUsed: 4,
  });

  assert.equal(svc.tenantSpaceInUse('a'), 0);
  assert.equal(svc.tenantSpaceInUse('b'), 0);
});

test('two tenants both complete with correct isolated results', async () => {
  const svc = new RuleService({ sliceSteps: 1 });
  const hA = svc.submit({
    tenantId: 'a',
    ast: heavyMap([1, 2, 3], 10),
    variables: { xs: [1, 2, 3] },
    budget: { steps: 100_000, space: 100 },
  });
  const hB = svc.submit({
    tenantId: 'b',
    ast: {
      kind: 'fold',
      list: v('xs'),
      param: 'x',
      acc: 'acc',
      init: int(100),
      body: add(v('acc'), v('x')),
    },
    variables: { xs: [1, 2, 3] },
    budget: { steps: 100_000, space: 100 },
  });
  const [rA, rB] = await Promise.all([hA.promise, hB.promise]);
  assert.equal(rA.ok, true);
  if (rA.ok) assert.deepEqual(rA.value, [1n, 2n, 3n]); // even neg depth: identity
  assert.equal(rB.ok, true);
  if (rB.ok) assert.equal(rB.value, 106n);
});
