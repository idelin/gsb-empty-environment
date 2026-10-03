import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateCompensation, selectEvents } from '../src/compensator.ts';
import type { CdcEvent } from '../src/types.ts';

function ev(partial: Partial<CdcEvent> & Pick<CdcEvent, 'sequence' | 'op'>): CdcEvent {
  return {
    txId: 'tx-1',
    timestampMs: 1000,
    table: 'users',
    primaryKey: { id: 1 },
    before: null,
    after: null,
    ...partial,
  };
}

test('UPDATE 生成反向赋值：SET 取 Before Image，WHERE 取主键', () => {
  const [stmt] = generateCompensation([
    ev({
      sequence: 1,
      op: 'UPDATE',
      before: { id: 1, name: 'alice', balance: 100 },
      after: { id: 1, name: null, balance: 0 },
    }),
  ]);
  assert.equal(stmt.kind, 'UPDATE');
  assert.deepEqual(stmt.set, { name: 'alice', balance: 100 });
  assert.deepEqual(stmt.where, { id: 1 });
});

test('UPDATE 只回滚真正变化的列', () => {
  const [stmt] = generateCompensation([
    ev({
      sequence: 1,
      op: 'UPDATE',
      before: { id: 1, name: 'alice', balance: 100 },
      after: { id: 1, name: 'alice', balance: 0 },
    }),
  ]);
  assert.deepEqual(stmt.set, { balance: 0 === 0 ? 100 : 0 });
});

test('DELETE 生成 INSERT（用 Before Image 重建）', () => {
  const [stmt] = generateCompensation([
    ev({ sequence: 1, op: 'DELETE', before: { id: 1, name: 'alice' } }),
  ]);
  assert.equal(stmt.kind, 'INSERT');
  assert.deepEqual(stmt.values, { id: 1, name: 'alice' });
});

test('INSERT 生成 DELETE（按主键冲正）', () => {
  const [stmt] = generateCompensation([
    ev({ sequence: 1, op: 'INSERT', after: { id: 1, name: 'alice' } }),
  ]);
  assert.equal(stmt.kind, 'DELETE');
  assert.deepEqual(stmt.where, { id: 1 });
});

test('同一行多次变更按 LIFO 逆序撤销', () => {
  const stmts = generateCompensation([
    ev({ sequence: 1, op: 'UPDATE', before: { id: 1, v: 'a' }, after: { id: 1, v: 'b' } }),
    ev({ sequence: 2, op: 'UPDATE', before: { id: 1, v: 'b' }, after: { id: 1, v: 'c' } }),
  ]);
  assert.deepEqual(stmts.map((s) => s.set), [{ v: 'b' }, { v: 'a' }]);
});

test('selectEvents 支持 txId 与毫秒时间窗过滤', () => {
  const log = [
    ev({ sequence: 1, op: 'INSERT', txId: 'tx-a', timestampMs: 100 }),
    ev({ sequence: 2, op: 'UPDATE', txId: 'tx-b', timestampMs: 200 }),
    ev({ sequence: 3, op: 'DELETE', txId: 'tx-b', timestampMs: 300 }),
  ];
  assert.equal(selectEvents(log, { txId: 'tx-b' }).length, 2);
  assert.equal(selectEvents(log, { fromMs: 150, toMs: 250 }).length, 1);
  assert.equal(selectEvents(log, {}).length, 3);
});

test('缺少镜像的 UPDATE 事件直接报错，拒绝生成不可靠补偿', () => {
  assert.throws(() => generateCompensation([ev({ sequence: 1, op: 'UPDATE' })]), /缺少镜像/);
});
