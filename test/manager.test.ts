import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  IdempotencyConflictError,
  InsufficientQuotaError,
  InvalidStateError,
  PermitMismatchError,
  PolicyDeniedError,
  StalePermitError,
  UnknownOperationError,
} from '../src/errors.ts';
import { QuotaManager, type Permit } from '../src/manager.ts';
import type { Rule } from '../src/policy.ts';

const ALLOW_ALL: Rule = { kind: 'in', attr: 'region', values: ['cn-north', 'cn-east'] };

function makeManager(rule: Rule = ALLOW_ALL): QuotaManager {
  const manager = new QuotaManager({ name: 'base', rule });
  manager.store.defineAccount('user-u1', { cpu: 10, memory: 32, gpu: 2 });
  manager.store.defineAccount('resource-r1', { slots: 5, bandwidth: 100 });
  return manager;
}

const ATTRS = { region: 'cn-north' };

function used(manager: QuotaManager, account: string, dimension: string): number {
  return manager.store.snapshot(account)[dimension].used;
}

describe('multi-dimensional atomic deduction', () => {
  it('deducts every dimension when all are sufficient', () => {
    const m = makeManager();
    const result = m.acquire({
      operationId: 'op-1',
      legs: [
        { account: 'user-u1', dimensions: { cpu: 4, memory: 8 } },
        { account: 'resource-r1', dimensions: { slots: 1 } },
      ],
      attributes: ATTRS,
    });
    assert.equal(result.status, 'reserved');
    assert.equal(used(m, 'user-u1', 'cpu'), 4);
    assert.equal(used(m, 'user-u1', 'memory'), 8);
    assert.equal(used(m, 'resource-r1', 'slots'), 1);
  });

  it('deducts nothing on any dimension when one dimension is insufficient', () => {
    const m = makeManager();
    assert.throws(
      () => m.acquire({
        operationId: 'op-2',
        legs: [{ account: 'user-u1', dimensions: { cpu: 4, gpu: 5 } }],
        attributes: ATTRS,
      }),
      (err: unknown) => {
        assert.ok(err instanceof InsufficientQuotaError);
        assert.deepEqual(err.shortages, [
          { account: 'user-u1', dimension: 'gpu', requested: 5, available: 2 },
        ]);
        return true;
      },
    );
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
    assert.equal(used(m, 'user-u1', 'gpu'), 0);
  });

  it('deducts neither user nor resource quota when resource quota is insufficient', () => {
    const m = makeManager();
    assert.throws(
      () => m.acquire({
        operationId: 'op-3',
        legs: [
          { account: 'user-u1', dimensions: { cpu: 2 } },
          { account: 'resource-r1', dimensions: { slots: 99 } },
        ],
        attributes: ATTRS,
      }),
      InsufficientQuotaError,
    );
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
    assert.equal(used(m, 'resource-r1', 'slots'), 0);
    assert.equal(m.statusOf('op-3'), undefined);
  });
});

describe('idempotency', () => {
  it('charges only once for retries with the same operation id', () => {
    const m = makeManager();
    const request = {
      operationId: 'op-10',
      legs: [{ account: 'user-u1', dimensions: { cpu: 3 } }],
      attributes: ATTRS,
    };
    const first = m.acquire(request);
    const second = m.acquire(request);
    const third = m.acquire({ ...request, legs: [...request.legs] });
    assert.equal(first.status, 'reserved');
    assert.equal(second.status, 'reserved');
    assert.equal(third.status, 'reserved');
    if (first.status === 'reserved' && second.status === 'reserved') {
      assert.deepEqual(second.permit, first.permit);
    }
    assert.equal(used(m, 'user-u1', 'cpu'), 3);
  });

  it('rejects the same operation id with different requirements', () => {
    const m = makeManager();
    m.acquire({
      operationId: 'op-11',
      legs: [{ account: 'user-u1', dimensions: { cpu: 3 } }],
      attributes: ATTRS,
    });
    assert.throws(
      () => m.acquire({
        operationId: 'op-11',
        legs: [{ account: 'user-u1', dimensions: { cpu: 4 } }],
        attributes: ATTRS,
      }),
      IdempotencyConflictError,
    );
    assert.throws(
      () => m.acquire({
        operationId: 'op-11',
        legs: [{ account: 'user-u1', dimensions: { cpu: 3 } }],
        attributes: { region: 'cn-east' },
      }),
      IdempotencyConflictError,
    );
    assert.equal(used(m, 'user-u1', 'cpu'), 3);
  });

  it('does not deduct again when a rolled-back operation id is retried', () => {
    const m = makeManager();
    const request = {
      operationId: 'op-12',
      legs: [{ account: 'user-u1', dimensions: { cpu: 3 } }],
      attributes: ATTRS,
    };
    m.acquire(request);
    m.cancel('op-12');
    const replay = m.acquire(request);
    assert.equal(replay.status, 'rolled-back');
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
  });
});

describe('cancel and failure rollback', () => {
  it('cancel restores every dimension and is idempotent', () => {
    const m = makeManager();
    m.acquire({
      operationId: 'op-20',
      legs: [
        { account: 'user-u1', dimensions: { cpu: 4, memory: 8 } },
        { account: 'resource-r1', dimensions: { slots: 2, bandwidth: 50 } },
      ],
      attributes: ATTRS,
    });
    m.cancel('op-20');
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
    assert.equal(used(m, 'user-u1', 'memory'), 0);
    assert.equal(used(m, 'resource-r1', 'slots'), 0);
    assert.equal(used(m, 'resource-r1', 'bandwidth'), 0);
    assert.deepEqual(m.cancel('op-20'), { status: 'rolled-back' });
  });

  it('fail restores every dimension like cancel', () => {
    const m = makeManager();
    m.acquire({
      operationId: 'op-21',
      legs: [{ account: 'user-u1', dimensions: { gpu: 1 } }],
      attributes: ATTRS,
    });
    m.fail('op-21');
    assert.equal(used(m, 'user-u1', 'gpu'), 0);
    assert.equal(m.statusOf('op-21'), 'rolled-back');
  });

  it('forbids rollback after commit and commit after rollback', () => {
    const m = makeManager();
    const req = {
      operationId: 'op-22',
      legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
      attributes: ATTRS,
    };
    const first = m.acquire(req);
    assert.ok(first.status === 'reserved');
    m.commit('op-22', first.permit);
    assert.throws(() => m.cancel('op-22'), InvalidStateError);

    const req2 = { ...req, operationId: 'op-23' };
    const second = m.acquire(req2);
    assert.ok(second.status === 'reserved');
    m.fail('op-23');
    assert.throws(() => m.commit('op-23', second.permit), InvalidStateError);
  });

  it('commit is idempotent and validates the permit', () => {
    const m = makeManager();
    const result = m.acquire({
      operationId: 'op-24',
      legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
      attributes: ATTRS,
    });
    assert.ok(result.status === 'reserved');
    assert.deepEqual(m.commit('op-24', result.permit), { status: 'committed' });
    assert.deepEqual(m.commit('op-24', result.permit), { status: 'committed' });
    const forged: Permit = { ...result.permit, requestHash: '0'.repeat(64) };
    assert.throws(() => m.commit('op-24', forged), PermitMismatchError);
    assert.throws(() => m.commit('op-404', result.permit), UnknownOperationError);
    assert.throws(() => m.cancel('op-404'), UnknownOperationError);
  });
});

describe('versioned policies and stale permits', () => {
  it('rejects permits issued under an older policy version and rolls back', () => {
    const m = makeManager();
    const result = m.acquire({
      operationId: 'op-30',
      legs: [
        { account: 'user-u1', dimensions: { cpu: 5 } },
        { account: 'resource-r1', dimensions: { slots: 1 } },
      ],
      attributes: ATTRS,
    });
    assert.ok(result.status === 'reserved');
    assert.equal(result.permit.policyVersion, 1);

    const v2 = m.updatePolicy('stricter', {
      kind: 'and',
      rules: [ALLOW_ALL, { kind: 'int', attr: 'seats', cmp: 'lte', value: 50 }],
    });
    assert.equal(v2.version, 2);
    assert.equal(m.currentPolicy.version, 2);

    assert.throws(
      () => m.commit('op-30', result.permit),
      (err: unknown) => {
        assert.ok(err instanceof StalePermitError);
        return true;
      },
    );
    assert.equal(m.statusOf('op-30'), 'rolled-back');
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
    assert.equal(used(m, 'resource-r1', 'slots'), 0);
  });

  it('accepts permits issued under the current version and keeps version history', () => {
    const m = makeManager();
    m.updatePolicy('v2', ALLOW_ALL);
    const result = m.acquire({
      operationId: 'op-31',
      legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
      attributes: ATTRS,
    });
    assert.ok(result.status === 'reserved');
    assert.equal(result.permit.policyVersion, 2);
    assert.deepEqual(m.commit('op-31', result.permit), { status: 'committed' });
    assert.equal(m.policyVersion(1)?.name, 'base');
    assert.equal(m.policyVersion(2)?.name, 'v2');
  });

  it('denies acquires that fail the policy without deducting', () => {
    const m = makeManager({
      kind: 'and',
      rules: [
        { kind: 'in', attr: 'region', values: ['cn-north'] },
        { kind: 'not', rule: { kind: 'in', attr: 'tier', values: ['blocked'] } },
      ],
    });
    assert.throws(
      () => m.acquire({
        operationId: 'op-32',
        legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
        attributes: { region: 'cn-north', tier: 'blocked' },
      }),
      PolicyDeniedError,
    );
    assert.throws(
      () => m.acquire({
        operationId: 'op-33',
        legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
        attributes: { region: 'eu-west' },
      }),
      PolicyDeniedError,
    );
    assert.equal(used(m, 'user-u1', 'cpu'), 0);
    assert.equal(m.statusOf('op-32'), undefined);
  });

  it('evaluates the new policy immediately after an update', () => {
    const m = makeManager();
    m.updatePolicy('small-only', { kind: 'int', attr: 'seats', cmp: 'lte', value: 4 });
    assert.throws(
      () => m.acquire({
        operationId: 'op-34',
        legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
        attributes: { seats: 10 },
      }),
      PolicyDeniedError,
    );
    const ok = m.acquire({
      operationId: 'op-35',
      legs: [{ account: 'user-u1', dimensions: { cpu: 1 } }],
      attributes: { seats: 2 },
    });
    assert.equal(ok.status, 'reserved');
  });
});
