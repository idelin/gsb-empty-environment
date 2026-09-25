import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { ObligationExecutor, type ObligationHandler } from '../src/executor.ts';
import type { Obligation } from '../src/types.ts';

const ob1: Obligation = { kind: 'quota', id: 'daily', limit: 5 };
const ob2: Obligation = { kind: 'mask', id: 'm1', field: 'email', format: 'hash' };
const ob3: Obligation = { kind: 'quota', id: 'weekly', limit: 20 };

function makeHandler(opts: { failExecute?: boolean; failCompensate?: { times: number } }): ObligationHandler & { calls: string[] } {
  const calls: string[] = [];
  let compensateFailuresLeft = opts.failCompensate?.times ?? 0;
  return {
    calls,
    execute(ob) {
      calls.push(`execute:${ob.kind}:${ob.id}`);
      if (opts.failExecute) throw new Error('execute failed');
    },
    compensate(ob) {
      calls.push(`compensate:${ob.kind}:${ob.id}`);
      if (compensateFailuresLeft > 0) {
        compensateFailuresLeft -= 1;
        throw new Error('compensate failed');
      }
    },
  };
}

describe('义务执行与逆序补偿', () => {
  it('全部成功 => completed，无补偿', () => {
    const executor = new ObligationExecutor({ quota: makeHandler({}), mask: makeHandler({}) });
    assert.equal(executor.run([ob1, ob2]), 'completed');
    assert.deepEqual(executor.compensated, []);
  });

  it('第二义务失败 => 按逆序补偿已执行义务', () => {
    const quotaHandler = makeHandler({});
    const maskHandler = makeHandler({ failExecute: true });
    const executor = new ObligationExecutor({ quota: quotaHandler, mask: maskHandler });

    const status = executor.run([ob1, ob2]);

    assert.equal(status, 'compensated');
    assert.deepEqual(executor.executed, ['quota:daily']);
    assert.deepEqual(executor.compensated, ['quota:daily']);
  });

  it('三个义务中第三个失败 => 逆序补偿第二、第一', () => {
    const order: string[] = [];
    const handler: ObligationHandler = {
      execute: (ob) => { order.push(`exec:${ob.id}`); if (ob.id === 'weekly') throw new Error('boom'); },
      compensate: (ob) => { order.push(`comp:${ob.id}`); },
    };
    const executor = new ObligationExecutor({ quota: handler, mask: handler });
    assert.equal(executor.run([ob1, ob2, ob3]), 'compensated');
    assert.deepEqual(order, ['exec:daily', 'exec:m1', 'exec:weekly', 'comp:m1', 'comp:daily']);
  });

  it('补偿失败 => 保留可重试状态，重试后完成补偿', () => {
    const quotaHandler = makeHandler({ failCompensate: { times: 1 } });
    const maskHandler = makeHandler({ failExecute: true });
    const executor = new ObligationExecutor({ quota: quotaHandler, mask: maskHandler });

    const status = executor.run([ob1, ob2]);
    assert.equal(status, 'compensation-failed');
    assert.equal(executor.pendingCompensation.length, 1);

    const retried = executor.retry();
    assert.equal(retried, 'compensated');
    assert.deepEqual(executor.compensated, ['quota:daily']);
  });

  it('补偿持续失败 => 状态保持 compensation-failed', () => {
    const quotaHandler = makeHandler({ failCompensate: { times: 3 } });
    const maskHandler = makeHandler({ failExecute: true });
    const executor = new ObligationExecutor({ quota: quotaHandler, mask: maskHandler });

    executor.run([ob1, ob2]);
    assert.equal(executor.retry(), 'compensation-failed');
    assert.equal(executor.retry(), 'compensation-failed');
    assert.equal(executor.retry(), 'compensated');
  });
});
