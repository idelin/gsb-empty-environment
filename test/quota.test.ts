import { test } from 'node:test';
import assert from 'node:assert/strict';
import { QuotaGuard, QuotaExceededError, RuleSandbox, BudgetExceeded } from '../src/index.ts';

test('enforces per-tenant concurrency limits', () => {
  const guard = new QuotaGuard({ maxConcurrent: 1 });
  const release = guard.acquire('tenant-a');
  assert.throws(() => guard.acquire('tenant-a'), QuotaExceededError);
  release(10);
  const again = guard.acquire('tenant-a');
  again(5);
});

test('tenants are isolated from each other', () => {
  const guard = new QuotaGuard({ maxConcurrent: 1 });
  const release = guard.acquire('tenant-a');
  const other = guard.acquire('tenant-b'); // unaffected by tenant-a
  other(0);
  release(0);
});

test('enforces evaluation-count quota per rolling window', () => {
  let now = 1_000_000;
  const guard = new QuotaGuard({ maxEvaluationsPerWindow: 2, windowMs: 1000, now: () => now });
  const sandbox = new RuleSandbox();
  assert.equal(guard.execute('t', sandbox, '1 + 1'), 2);
  assert.equal(guard.execute('t', sandbox, '2 + 2'), 4);
  assert.throws(() => guard.execute('t', sandbox, '3 + 3'), QuotaExceededError);
  now += 1001; // window rolls over
  assert.equal(guard.execute('t', sandbox, '3 + 3'), 6);
});

test('enforces cumulative step budget per window, billing failed runs too', () => {
  let now = 0;
  const guard = new QuotaGuard({ maxStepsPerWindow: 1001, windowMs: 60_000, now: () => now });
  const sandbox = new RuleSandbox({ maxSteps: 1000, timeoutMs: 10_000 });

  // The malicious expression burns the tenant's whole 1000-step budget...
  assert.throws(() => guard.execute('attacker', sandbox, 'eval(self)'), BudgetExceeded);
  // ...and a second attempt pushes the tenant over the per-window step quota.
  assert.throws(() => guard.execute('attacker', sandbox, 'eval(self)'), QuotaExceededError);
  // Meanwhile another tenant is completely unaffected.
  assert.equal(guard.execute('honest', sandbox, '6 * 7'), 42);
});

test('release is idempotent', () => {
  const guard = new QuotaGuard({ maxConcurrent: 1 });
  const release = guard.acquire('t');
  release(1);
  release(1);
  const again = guard.acquire('t');
  again(0);
});
