import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ReplayScheduler, ReplayDeadlockError, type CdcEvent, type SuspendedRecord } from '../src/scheduler.ts';
import { ForeignKeyViolationError, TargetDatabase } from '../src/target-db.ts';
import { makeGraph } from './helpers.ts';

function setup(options: { concurrency?: number; applyDelayMs?: number } = {}) {
  const graph = makeGraph();
  const db = new TargetDatabase(graph);
  const suspended: SuspendedRecord[] = [];
  const released: SuspendedRecord[] = [];
  const scheduler = new ReplayScheduler(graph, db, {
    concurrency: options.concurrency ?? 4,
    applyDelayMs: options.applyDelayMs ?? 0,
    onSuspend: (record) => suspended.push(record),
    onRelease: (record) => released.push(record),
  });
  return { graph, db, scheduler, suspended, released };
}

const userInsert: CdcEvent = { id: 'e-user', table: 'User', op: 'insert', row: { id: 1, name: 'alice' } };
const orderInsert: CdcEvent = { id: 'e-order', table: 'Order', op: 'insert', row: { id: 10, userId: 1 } };
const itemInsert: CdcEvent = { id: 'e-item', table: 'OrderItem', op: 'insert', row: { id: 100, orderId: 10, sku: 'book' } };

test('acceptance: out-of-order batch (OrderItem -> Order -> User) is reordered by FK topology', async () => {
  const { db, scheduler, suspended, released } = setup({ concurrency: 4 });

  const report = await scheduler.replay([itemInsert, orderInsert, userInsert]);

  assert.deepEqual(
    report.applied.map((r) => r.table),
    ['User', 'Order', 'OrderItem'],
    'parent rows must be applied before child rows',
  );
  assert.deepEqual(
    suspended.map((s) => s.eventId).sort(),
    ['e-item', 'e-order'],
    'child inserts must be suspended while parents are missing',
  );
  assert.deepEqual(
    released.map((s) => s.eventId).sort(),
    ['e-item', 'e-order'],
    'suspended events must be released once parents land',
  );
  const itemSuspension = suspended.find((s) => s.eventId === 'e-item')!;
  assert.deepEqual(itemSuspension.waitingFor, [{ table: 'Order', key: '[10]' }]);
  assert.equal(db.count('User'), 1);
  assert.equal(db.count('Order'), 1);
  assert.equal(db.count('OrderItem'), 1);
});

test('acceptance: scheduler never lets the target DB raise an FK violation', async () => {
  const { db, scheduler } = setup({ concurrency: 8 });
  const events: CdcEvent[] = [];
  for (let i = 1; i <= 20; i += 1) {
    events.push({ id: `item-${i}`, table: 'OrderItem', op: 'insert', row: { id: 1000 + i, orderId: i } });
    events.push({ id: `order-${i}`, table: 'Order', op: 'insert', row: { id: i, userId: i } });
    events.push({ id: `user-${i}`, table: 'User', op: 'insert', row: { id: i, name: `u${i}` } });
  }
  const report = await scheduler.replay(events);
  assert.equal(report.applied.length, 60);
  assert.equal(db.count('OrderItem'), 20);
  const appliedIndex = new Map(report.applied.map((r, i) => [r.eventId, i]));
  for (let i = 1; i <= 20; i += 1) {
    assert.ok(appliedIndex.get(`user-${i}`)! < appliedIndex.get(`order-${i}`)!);
    assert.ok(appliedIndex.get(`order-${i}`)! < appliedIndex.get(`item-${i}`)!);
  }
});

test('deletes are applied children-first even when submitted parents-first', async () => {
  const { db, scheduler } = setup({ concurrency: 4 });
  db.insert('User', { id: 1, name: 'alice' });
  db.insert('Order', { id: 10, userId: 1 });
  db.insert('OrderItem', { id: 100, orderId: 10 });

  const report = await scheduler.replay([
    { id: 'd-user', table: 'User', op: 'delete', key: '[1]' },
    { id: 'd-order', table: 'Order', op: 'delete', key: '[10]' },
    { id: 'd-item', table: 'OrderItem', op: 'delete', key: '[100]' },
  ]);

  assert.deepEqual(
    report.applied.map((r) => r.eventId),
    ['d-item', 'd-order', 'd-user'],
  );
  assert.equal(db.count('User'), 0);
  assert.equal(db.count('Order'), 0);
  assert.equal(db.count('OrderItem'), 0);
});

test('events whose parents already exist in the target DB are not suspended', async () => {
  const { db, scheduler, suspended } = setup();
  db.insert('User', { id: 1, name: 'pre-existing' });

  const report = await scheduler.replay([orderInsert]);

  assert.equal(suspended.length, 0);
  assert.deepEqual(report.applied.map((r) => r.eventId), ['e-order']);
});

test('update events wait for FK parents just like inserts', async () => {
  const { db, scheduler } = setup();
  db.insert('User', { id: 9, name: 'old-parent' });
  db.insert('Order', { id: 10, userId: 9 });

  const report = await scheduler.replay([
    { id: 'u-order', table: 'Order', op: 'update', row: { id: 10, userId: 1 } },
    userInsert,
  ]);

  assert.deepEqual(
    report.applied.map((r) => r.eventId),
    ['e-user', 'u-order'],
  );
  assert.deepEqual(db.get('Order', '[10]'), { id: 10, userId: 1 });
});

test('a batch that can never be satisfied raises a deadlock with diagnostics', async () => {
  const { scheduler } = setup();
  await assert.rejects(
    scheduler.replay([itemInsert]),
    (error: unknown) => {
      assert.ok(error instanceof ReplayDeadlockError);
      assert.equal(error.suspended.length, 1);
      assert.deepEqual(error.suspended[0]!.waitingFor, [{ table: 'Order', key: '[10]' }]);
      return true;
    },
  );
});

test('independent branches replay concurrently up to the concurrency limit', async () => {
  const { scheduler } = setup({ concurrency: 3, applyDelayMs: 30 });
  const events: CdcEvent[] = [];
  for (let i = 1; i <= 6; i += 1) {
    events.push({ id: `user-${i}`, table: 'User', op: 'insert', row: { id: i } });
  }
  const report = await scheduler.replay(events);
  assert.equal(report.maxConcurrency, 3, 'three independent inserts should be in flight at once');
  assert.equal(report.applied.length, 6);
});

test('raw out-of-order writes without the scheduler do violate FK constraints (control group)', () => {
  const graph = makeGraph();
  const db = new TargetDatabase(graph);
  assert.throws(() => db.insert('OrderItem', { id: 100, orderId: 10 }), ForeignKeyViolationError);
  db.insert('User', { id: 1 });
  db.insert('Order', { id: 10, userId: 1 });
  db.insert('OrderItem', { id: 100, orderId: 10 });
  assert.throws(() => db.delete('Order', '[10]'), ForeignKeyViolationError);
});
