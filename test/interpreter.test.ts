import test from 'node:test';
import assert from 'node:assert/strict';
import { RuleService } from '../src/interpreter.ts';
import type { AstNode, Budget, EvalResult } from '../src/interpreter.ts';

const int = (value: number | string): AstNode => ({ kind: 'int', value });
const bool = (value: boolean): AstNode => ({ kind: 'bool', value });
const ref = (name: string): AstNode => ({ kind: 'var', name });
const add = (a: AstNode, b: AstNode): AstNode => ({ kind: 'arith', op: 'add', args: [a, b] });

const NO_BUDGET: Budget = { steps: 100_000_000, space: 1_000_000 };

function run(
  service: RuleService,
  ast: AstNode,
  opts: { tenantId?: string; vars?: Record<string, unknown>; budget?: Budget } = {},
): Promise<EvalResult> {
  return service.submit({
    tenantId: opts.tenantId ?? 't1',
    ast,
    vars: opts.vars ?? {},
    budget: opts.budget ?? NO_BUDGET,
  }).promise;
}

async function waitFor(cond: () => boolean, what: string): Promise<void> {
  for (let i = 0; i < 10_000; i++) {
    if (cond()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`timed out waiting for ${what}`);
}

function slowBody(depth: number): AstNode {
  let body: AstNode = ref('x');
  for (let i = 0; i < depth; i++) body = { kind: 'neg', arg: body };
  return body;
}

test('short-circuit logic and IF do not visit untaken branches', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000 });

  const orR = await run(svc, { kind: 'logic', op: 'or', args: [bool(true), ref('missing')] });
  assert.deepEqual(orR, { ok: true, value: true, steps: 2, space: 0 });

  const andR = await run(svc, { kind: 'logic', op: 'and', args: [bool(false), ref('missing')] });
  assert.deepEqual(andR, { ok: true, value: false, steps: 2, space: 0 });

  const ifR = await run(svc, { kind: 'if', cond: bool(true), then: int(1), else: ref('missing') });
  assert.deepEqual(ifR, { ok: true, value: 1n, steps: 3, space: 0 });

  const orFull = await run(svc, { kind: 'logic', op: 'or', args: [bool(false), bool(true)] });
  assert.deepEqual(orFull, { ok: true, value: true, steps: 3, space: 0 });
});

test('step budget: exact fit succeeds, one step less fails with AST path', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000 });
  const ast = add(int(1), int(2));

  const exact = await run(svc, ast, { budget: { steps: 3, space: 0 } });
  assert.deepEqual(exact, { ok: true, value: 3n, steps: 3, space: 0 });

  const short = await run(svc, ast, { budget: { steps: 2, space: 0 } });
  assert.equal(short.ok, false);
  if (!short.ok) {
    assert.equal(short.error.type, 'budget-steps');
    assert.equal(short.error.path, '$.args[1]');
  }
});

test('map space budget: exact fit succeeds, one element less fails', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000 });
  const ast: AstNode = { kind: 'map', array: ref('xs'), param: 'x', body: ref('x') };
  const vars = { xs: [1, 2, 3] };

  const exact = await run(svc, ast, { vars, budget: { steps: 8, space: 3 } });
  assert.deepEqual(exact, { ok: true, value: [1n, 2n, 3n], steps: 8, space: 3 });

  const shortSpace = await run(svc, ast, { vars, budget: { steps: 8, space: 2 } });
  assert.equal(shortSpace.ok, false);
  if (!shortSpace.ok) {
    assert.equal(shortSpace.error.type, 'budget-space');
    assert.equal(shortSpace.error.path, '$');
  }

  const shortSteps = await run(svc, ast, { vars, budget: { steps: 7, space: 3 } });
  assert.equal(shortSteps.ok, false);
  if (!shortSteps.ok) assert.equal(shortSteps.error.type, 'budget-steps');
});

test('map/filter/fold semantics', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000 });
  const vars = { xs: [1, 2, 3, 4, 5] };

  const mapped = await run(svc, {
    kind: 'map', array: ref('xs'), param: 'x',
    body: { kind: 'arith', op: 'mul', args: [ref('x'), int(2)] },
  }, { vars });
  assert.deepEqual(mapped.ok && mapped.value, [2n, 4n, 6n, 8n, 10n]);

  const filtered = await run(svc, {
    kind: 'filter', array: ref('xs'), param: 'x',
    body: { kind: 'cmp', op: 'gt', args: [ref('x'), int(2)] },
  }, { vars });
  assert.deepEqual(filtered.ok && filtered.value, [3n, 4n, 5n]);
  assert.equal(filtered.ok && filtered.space, 3);

  const folded = await run(svc, {
    kind: 'fold', array: ref('xs'), param: 'x', acc: 'acc', init: int(0),
    body: add(ref('acc'), ref('x')),
  }, { vars });
  assert.deepEqual(folded.ok && folded.value, 15n);
  assert.equal(folded.ok && folded.space, 0);
});

test('deep nesting runs on an explicit stack without call-stack overflow', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000, chunkSize: 4096 });
  const depth = 200_000;
  let ast: AstNode = int(7);
  for (let i = 0; i < depth; i++) ast = { kind: 'neg', arg: ast };

  const result = await run(svc, ast, { budget: { steps: depth + 1, space: 0 } });
  assert.deepEqual(result, { ok: true, value: 7n, steps: depth + 1, space: 0 });
});

test('overflow, type errors and missing vars report their AST path', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 1000 });

  const overflow = await run(svc, add(int('9223372036854775807'), int(1)));
  assert.equal(overflow.ok, false);
  if (!overflow.ok) {
    assert.equal(overflow.error.type, 'overflow');
    assert.equal(overflow.error.path, '$');
  }

  const typeErr = await run(svc, add(int(1), bool(true)));
  assert.equal(typeErr.ok, false);
  if (!typeErr.ok) {
    assert.equal(typeErr.error.type, 'type');
    assert.equal(typeErr.error.path, '$');
  }

  const nested = await run(svc, { kind: 'if', cond: bool(false), then: int(1), else: add(int(1), bool(true)) });
  assert.equal(nested.ok, false);
  if (!nested.ok) {
    assert.equal(nested.error.type, 'type');
    assert.equal(nested.error.path, '$.else');
  }

  const unbound = await run(svc, add(ref('nope'), int(1)));
  assert.equal(unbound.ok, false);
  if (!unbound.ok) {
    assert.equal(unbound.error.type, 'unbound-var');
    assert.equal(unbound.error.path, '$.args[0]');
  }

  const divZero = await run(svc, { kind: 'arith', op: 'div', args: [int(1), int(0)] });
  assert.equal(divZero.ok, false);
  if (!divZero.ok) assert.equal(divZero.error.type, 'domain');
});

test('cancelled request releases tenant space so it can be reused', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 100, chunkSize: 16 });
  const xs = Array.from({ length: 60 }, (_, i) => i);
  const slowAst: AstNode = { kind: 'map', array: ref('xs'), param: 'x', body: slowBody(2000) };
  const quickAst: AstNode = { kind: 'map', array: ref('xs'), param: 'x', body: ref('x') };

  const first = svc.submit({ tenantId: 'tA', ast: slowAst, vars: { xs }, budget: { steps: 10_000_000, space: 60 } });
  await waitFor(() => svc.inFlight('tA') === 60, 'tenant A reservation');

  const blocked = await svc.submit({ tenantId: 'tA', ast: quickAst, vars: { xs }, budget: { steps: 1000, space: 60 } }).promise;
  assert.equal(blocked.ok, false);
  if (!blocked.ok) assert.equal(blocked.error.type, 'tenant-space');

  first.cancel();
  const cancelled = await first.promise;
  assert.equal(cancelled.ok, false);
  if (!cancelled.ok) assert.equal(cancelled.error.type, 'cancelled');
  assert.equal(svc.inFlight('tA'), 0);

  const reused = await svc.submit({ tenantId: 'tA', ast: quickAst, vars: { xs }, budget: { steps: 1000, space: 60 } }).promise;
  assert.equal(reused.ok, true);
  if (reused.ok) assert.equal((reused.value as unknown[]).length, 60);
  assert.equal(svc.inFlight('tA'), 0);
});

test('failed request releases tenant reservation', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 100, chunkSize: 16 });
  const xs = Array.from({ length: 60 }, (_, i) => i);
  const badAst: AstNode = { kind: 'map', array: ref('xs'), param: 'x', body: add(ref('x'), bool(true)) };

  const failed = await svc.submit({ tenantId: 'tB', ast: badAst, vars: { xs }, budget: { steps: 1000, space: 60 } }).promise;
  assert.equal(failed.ok, false);
  if (!failed.ok) {
    assert.equal(failed.error.type, 'type');
    assert.equal(failed.error.path, '$.body[0]');
  }
  assert.equal(svc.inFlight('tB'), 0);

  const after = await svc.submit({
    tenantId: 'tB',
    ast: { kind: 'map', array: ref('xs'), param: 'x', body: ref('x') },
    vars: { xs },
    budget: { steps: 1000, space: 60 },
  }).promise;
  assert.equal(after.ok, true);
});

test('two tenants interleave with independent quotas and cancel isolation', async () => {
  const svc = new RuleService({ tenantSpaceLimit: 100, chunkSize: 8 });
  const xs = Array.from({ length: 60 }, (_, i) => i);
  const slowAst: AstNode = { kind: 'map', array: ref('xs'), param: 'x', body: slowBody(500) };
  const budget: Budget = { steps: 10_000_000, space: 60 };

  const a = svc.submit({ tenantId: 'tenant-a', ast: slowAst, vars: { xs }, budget });
  const b = svc.submit({ tenantId: 'tenant-b', ast: slowAst, vars: { xs }, budget });

  await waitFor(
    () => svc.inFlight('tenant-a') === 60 && svc.inFlight('tenant-b') === 60,
    'both tenants in flight',
  );

  a.cancel();
  const [ra, rb] = await Promise.all([a.promise, b.promise]);

  assert.equal(ra.ok, false);
  if (!ra.ok) assert.equal(ra.error.type, 'cancelled');

  assert.equal(rb.ok, true);
  if (rb.ok) assert.equal((rb.value as unknown[]).length, 60);

  assert.equal(svc.inFlight('tenant-a'), 0);
  assert.equal(svc.inFlight('tenant-b'), 0);
});
