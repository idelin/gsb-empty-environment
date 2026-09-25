import { QuotaManager, type Permit } from '../src/index.ts';

const manager = new QuotaManager({
  name: 'base-v1',
  rule: {
    kind: 'and',
    rules: [
      { kind: 'in', attr: 'region', values: ['cn-north', 'cn-east'] },
      { kind: 'int', attr: 'seats', cmp: 'lte', value: 100 },
      { kind: 'not', rule: { kind: 'in', attr: 'tier', values: ['blocked'] } },
    ],
  },
});

manager.store.defineAccount('user-alice', { cpu: 8, memory: 16 });
manager.store.defineAccount('gpu-cluster', { slots: 2 });

const show = (label: string): void => {
  console.log(`\n== ${label} ==`);
  console.log('user-alice :', manager.store.snapshot('user-alice'));
  console.log('gpu-cluster:', manager.store.snapshot('gpu-cluster'));
};

const attempt = (label: string, fn: () => unknown): void => {
  try {
    console.log(`\n-- ${label}\n   ->`, fn());
  } catch (err) {
    console.log(`\n-- ${label}\n   -> [${(err as Error).name}] ${(err as Error).message}`);
  }
};

show('initial state');

const request = {
  operationId: 'deploy-001',
  legs: [
    { account: 'user-alice', dimensions: { cpu: 4, memory: 8 } },
    { account: 'gpu-cluster', dimensions: { slots: 1 } },
  ],
  attributes: { region: 'cn-north', seats: 20, tier: 'pro' },
};

let permit: Permit;
attempt('acquire deploy-001 (all dimensions sufficient)', () => {
  const result = manager.acquire(request);
  if (result.status !== 'reserved') throw new Error('unexpected');
  permit = result.permit;
  return result;
});
show('after acquire');

attempt('retry acquire deploy-001 with identical request (idempotent)', () =>
  manager.acquire(request));
show('after identical retry: still charged only once');

attempt('retry deploy-001 with different requirements (conflict)', () =>
  manager.acquire({ ...request, legs: [{ account: 'user-alice', dimensions: { cpu: 5 } }] }));

attempt('acquire deploy-002: user quota ok but resource quota insufficient', () =>
  manager.acquire({
    operationId: 'deploy-002',
    legs: [
      { account: 'user-alice', dimensions: { cpu: 1 } },
      { account: 'gpu-cluster', dimensions: { slots: 5 } },
    ],
    attributes: { region: 'cn-north', seats: 1 },
  }));
show('after deploy-002 rejected: neither side was charged');

attempt('acquire deploy-003 denied by policy (tier=blocked)', () =>
  manager.acquire({
    operationId: 'deploy-003',
    legs: [{ account: 'user-alice', dimensions: { cpu: 1 } }],
    attributes: { region: 'cn-north', seats: 1, tier: 'blocked' },
  }));

attempt('cancel deploy-002 (never reserved)', () => manager.cancel('deploy-002'));

attempt('policy update to v2 (seats <= 10)', () =>
  manager.updatePolicy('stricter-v2', {
    kind: 'and',
    rules: [
      { kind: 'in', attr: 'region', values: ['cn-north', 'cn-east'] },
      { kind: 'int', attr: 'seats', cmp: 'lte', value: 10 },
    ],
  }));

attempt('commit deploy-001 with v1 permit (stale -> rejected + rolled back)', () =>
  manager.commit('deploy-001', permit));
show('after stale permit rejection: reservation fully rolled back');

attempt('re-acquire deploy-004 under policy v2 and commit', () => {
  const result = manager.acquire({
    operationId: 'deploy-004',
    legs: [{ account: 'user-alice', dimensions: { cpu: 2 } }],
    attributes: { region: 'cn-east', seats: 5 },
  });
  if (result.status !== 'reserved') throw new Error('unexpected');
  return manager.commit('deploy-004', result.permit);
});

attempt('fail deploy-004 after commit (not allowed)', () => manager.fail('deploy-004'));

attempt('acquire deploy-005 then cancel (rollback all dimensions)', () => {
  manager.acquire({
    operationId: 'deploy-005',
    legs: [
      { account: 'user-alice', dimensions: { memory: 4 } },
      { account: 'gpu-cluster', dimensions: { slots: 1 } },
    ],
    attributes: { region: 'cn-north', seats: 3 },
  });
  return manager.cancel('deploy-005');
});

show('final state');
